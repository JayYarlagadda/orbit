package processtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func buildBinaryTo(dir, packagePath, name string) (string, error) {
	fileName := name
	if runtime.GOOS == "windows" {
		fileName += ".exe"
	}
	output := filepath.Join(dir, fileName)
	cmd := exec.Command("go", "build", "-o", output, packagePath)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go build %s: %w\n%s", packagePath, err, out)
	}
	return output, nil
}

func freeAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address
}

func waitHealthy(t *testing.T, address string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		response, err := grpc_health_v1.NewHealthClient(connection).Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		cancel()
		_ = connection.Close()
		if err == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return
		}
		last = err
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("process at %s never became healthy: %v", address, last)
}

func waitExit(t *testing.T, cmd *exec.Cmd, timeout time.Duration) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process exited with error: %v", err)
		}
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		t.Fatalf("process did not exit within %s", timeout)
	}
}

type holdingControlServer struct {
	orbitv1.UnimplementedGatewayControlServiceServer
}

func (s *holdingControlServer) Connect(stream orbitv1.GatewayControlService_ConnectServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

type holdingDeviceServer struct {
	orbitv1.UnimplementedDeviceServiceServer
}

func (s *holdingDeviceServer) Connect(stream orbitv1.DeviceService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return fmt.Errorf("first device frame must be hello")
	}
	if err := stream.Send(&orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_SessionOpened{
		SessionOpened: &orbitv1.SessionOpened{DeviceId: hello.DeviceId, SessionEpoch: 1},
	}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return nil
}

func serveGRPC(t *testing.T, register func(*grpc.Server)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	register(server)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String()
}
