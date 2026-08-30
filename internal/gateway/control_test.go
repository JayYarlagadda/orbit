package gateway

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type closingControlServer struct {
	orbitv1.UnimplementedGatewayControlServiceServer
	connects atomic.Int32
}

func (s *closingControlServer) Connect(stream orbitv1.GatewayControlService_ConnectServer) error {
	s.connects.Add(1)
	if _, err := stream.Recv(); err != nil {
		return err
	}
	return nil
}

func startControlLoop(t *testing.T, server orbitv1.GatewayControlServiceServer) (orbitv1.GatewayControlServiceClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	orbitv1.RegisterGatewayControlServiceServer(grpcServer, server)
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- grpcServer.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///orbit-control",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	stop := func() {
		_ = connection.Close()
		grpcServer.Stop()
		_ = listener.Close()
		<-serveErrors
	}
	return orbitv1.NewGatewayControlServiceClient(connection), stop
}

func TestRunControlReconnectsUntilCanceled(t *testing.T) {
	control := &closingControlServer{}
	client, stop := startControlLoop(t, control)
	defer stop()
	hub := newTestHub(t, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- RunControl(ctx, client, hub, ControlConfig{
			GatewayInstanceID: "instance-1",
			InitialDelay:      10 * time.Millisecond,
			MaxDelay:          20 * time.Millisecond,
			Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	deadline := time.Now().Add(testTimeout)
	for control.connects.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := control.connects.Load(); got < 3 {
		t.Fatalf("control connects = %d, want at least 3", got)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunControl() error = %v, want context.Canceled", err)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for RunControl to return")
	}
}

func TestRunControlStopsAfterMaxAttempts(t *testing.T) {
	control := &closingControlServer{}
	client, stop := startControlLoop(t, control)
	defer stop()
	hub := newTestHub(t, 4)

	err := RunControl(context.Background(), client, hub, ControlConfig{
		GatewayInstanceID: "instance-1",
		InitialDelay:      10 * time.Millisecond,
		MaxDelay:          20 * time.Millisecond,
		MaxAttempts:       1,
		Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err == nil {
		t.Fatal("RunControl() succeeded with MaxAttempts=1")
	}
	if got := control.connects.Load(); got != 2 {
		t.Fatalf("control connects = %d, want 2 (initial plus one retry)", got)
	}
}

func TestRunControlStreamFailsHubOnDisconnect(t *testing.T) {
	control := &closingControlServer{}
	client, stop := startControlLoop(t, control)
	defer stop()
	hub := newTestHub(t, 4)

	err := RunControlStream(context.Background(), client, hub, "instance-1", heartbeat.Settings{})
	if !errors.Is(err, ErrControlDisconnected) {
		t.Fatalf("RunControlStream() error = %v, want ErrControlDisconnected", err)
	}
	select {
	case <-hub.Done():
	default:
		t.Fatal("hub was not failed after the control stream ended")
	}
}
