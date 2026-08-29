package gateway

import (
	"context"
	"crypto/sha256"
	"io"
	"net"
	"runtime"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func startDeviceLoop(t *testing.T, hub *Hub) (orbitv1.DeviceServiceClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1024 * 1024)
	grpcServer := grpc.NewServer()
	orbitv1.RegisterDeviceServiceServer(grpcServer, NewDeviceService(hub))
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- grpcServer.Serve(listener) }()
	connection, err := grpc.NewClient(
		"passthrough:///orbit-device",
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
	return orbitv1.NewDeviceServiceClient(connection), stop
}

func awaitOutbound(t *testing.T, hub *Hub) *orbitv1.GatewayFrame {
	t.Helper()
	select {
	case frame := <-hub.Outbound():
		return frame
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for a gateway outbound frame")
		return nil
	}
}

func openDeviceStream(t *testing.T, hub *Hub, client orbitv1.DeviceServiceClient, epoch int64) (orbitv1.DeviceService_ConnectClient, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	t.Cleanup(cancel)
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Hello{
		Hello: &orbitv1.DeviceHello{DeviceId: "device-1", ClientInstanceId: "client-1"},
	}}); err != nil {
		t.Fatalf("Send(hello) error = %v", err)
	}
	online := awaitOutbound(t, hub).GetDeviceOnline()
	if online == nil || online.DeviceId != "device-1" {
		t.Fatalf("first outbound frame = %+v, want DeviceOnline", online)
	}
	if err := hub.Deliver(ctx, sessionFrame(online.ConnectionId, "device-1", epoch)); err != nil {
		t.Fatalf("Deliver(session) error = %v", err)
	}
	opened, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(session) error = %v", err)
	}
	session := opened.GetSessionOpened()
	if session == nil || session.DeviceId != "device-1" || session.SessionEpoch != epoch {
		t.Fatalf("SessionOpened = %+v, want epoch %d", opened, epoch)
	}
	return stream, online.ConnectionId
}

func TestDeviceServiceForwardsAssignmentAndAcknowledgement(t *testing.T) {
	hub := newTestHub(t, 4)
	client, stop := startDeviceLoop(t, hub)
	defer stop()
	stream, connectionID := openDeviceStream(t, hub, client, 7)

	payload := []byte("collect")
	hash := sha256.Sum256(payload)
	delivery := &orbitv1.CommandDelivery{
		CommandId:      "command-1",
		DeviceId:       "device-1",
		SequenceNumber: 1,
		Payload:        payload,
		PayloadHash:    hash[:],
		LeaseToken:     3,
		SessionEpoch:   7,
	}
	if err := hub.Deliver(context.Background(), &orbitv1.ControlFrame{
		Body: &orbitv1.ControlFrame_CommandAssignment{CommandAssignment: &orbitv1.GatewayCommandAssignment{
			ConnectionId: connectionID,
			Command:      delivery,
		}},
	}); err != nil {
		t.Fatalf("Deliver(assignment) error = %v", err)
	}

	frame, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv(command) error = %v", err)
	}
	got := frame.GetCommand()
	if got == nil || got.CommandId != "command-1" || got.SessionEpoch != 7 {
		t.Fatalf("command = %+v", frame)
	}

	started := awaitOutbound(t, hub).GetDeliveryStarted()
	if started == nil || started.CommandId != "command-1" || started.LeaseToken != 3 {
		t.Fatalf("DeliveryStarted = %+v", started)
	}

	if err := stream.Send(&orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Ack{Ack: &orbitv1.CommandAck{
		CommandId:      "command-1",
		DeviceId:       "device-1",
		SequenceNumber: 1,
		LeaseToken:     3,
		SessionEpoch:   7,
		ResultHash:     hash[:],
	}}}); err != nil {
		t.Fatalf("Send(ack) error = %v", err)
	}
	ackFrame := awaitOutbound(t, hub).GetCommandAck()
	if ackFrame == nil || ackFrame.Ack == nil || ackFrame.Ack.CommandId != "command-1" {
		t.Fatalf("CommandAck = %+v", ackFrame)
	}

	if err := stream.CloseSend(); err != nil {
		t.Fatalf("CloseSend() error = %v", err)
	}
	if _, err := stream.Recv(); err != io.EOF && status.Code(err) != codes.Canceled && status.Code(err) != codes.Unavailable {
		if err != nil {
			t.Fatalf("Recv after CloseSend error = %v", err)
		}
	}
	offline := awaitOutbound(t, hub).GetDeviceOffline()
	if offline == nil || offline.ConnectionId != connectionID || offline.SessionEpoch != 7 {
		t.Fatalf("DeviceOffline = %+v", offline)
	}
}

func TestDeviceServiceRejectsNonHelloFirstFrame(t *testing.T) {
	hub := newTestHub(t, 4)
	client, stop := startDeviceLoop(t, hub)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	stream, err := client.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Ack{Ack: &orbitv1.CommandAck{
		CommandId: "command-1", DeviceId: "device-1", SequenceNumber: 1, SessionEpoch: 1,
	}}}); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	_, err = stream.Recv()
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("Recv() error = %v, want InvalidArgument", err)
	}
}

func TestDeviceServiceEndsWhenControlRebinds(t *testing.T) {
	hub := newTestHub(t, 4)
	client, stop := startDeviceLoop(t, hub)
	defer stop()
	stream, _ := openDeviceStream(t, hub, client, 2)

	hub.Rebind()
	_, err := stream.Recv()
	if status.Code(err) != codes.Unavailable {
		t.Fatalf("Recv() after Rebind error = %v, want Unavailable", err)
	}
	select {
	case frame := <-hub.Outbound():
		t.Fatalf("rebind leaked outbound frame %+v", frame)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestDeviceServiceConnectsDoNotLeakGoroutines(t *testing.T) {
	hub := newTestHub(t, 4)
	client, stop := startDeviceLoop(t, hub)
	defer stop()

	runtime.GC()
	baseline := runtime.NumGoroutine()
	for range 12 {
		ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
		stream, err := client.Connect(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := stream.Send(&orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Hello{
			Hello: &orbitv1.DeviceHello{DeviceId: "device-1", ClientInstanceId: "client-1"},
		}}); err != nil {
			cancel()
			t.Fatalf("Send(hello) error = %v", err)
		}
		online := awaitOutbound(t, hub).GetDeviceOnline()
		if online == nil {
			cancel()
			t.Fatal("missing DeviceOnline")
		}
		if err := hub.Deliver(ctx, sessionFrame(online.ConnectionId, "device-1", 1)); err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, err := stream.Recv(); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		_, _ = stream.Recv()
		select {
		case <-hub.Outbound():
		case <-time.After(testTimeout):
			t.Fatal("timed out waiting for DeviceOffline")
		}
	}
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		runtime.GC()
		if runtime.NumGoroutine() <= baseline+8 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutines = %d, baseline = %d", runtime.NumGoroutine(), baseline)
}
