package processtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
)

func TestGatewayGracefulShutdown(t *testing.T) {
	controlAddress := serveGRPC(t, func(server *grpc.Server) {
		orbitv1.RegisterGatewayControlServiceServer(server, &holdingControlServer{})
	})
	listenAddress := freeAddress(t)
	binary := gatewayBinary

	cmd := exec.Command(binary)
	cmd.Env = withTelemetryOff(append(os.Environ(),
		"ORBIT_GATEWAY_ID=gateway-shutdown",
		"ORBIT_CONTROL_ADDRESS="+controlAddress,
		"ORBIT_GATEWAY_LISTEN_ADDRESS="+listenAddress,
		"ORBIT_GATEWAY_SHUTDOWN_TIMEOUT=2s",
	))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	prepareInterruptible(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitHealthy(t, listenAddress, 10*time.Second)
	if err := interrupt(cmd); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitExit(t, cmd, 8*time.Second)
}

func TestClientGracefulShutdown(t *testing.T) {
	gatewayAddress := serveGRPC(t, func(server *grpc.Server) {
		orbitv1.RegisterDeviceServiceServer(server, &holdingDeviceServer{})
	})
	binary := clientBinary
	statePath := filepath.Join(t.TempDir(), "state.json")

	cmd := exec.Command(binary)
	cmd.Env = withTelemetryOff(append(os.Environ(),
		"ORBIT_DEVICE_ID=edge-shutdown",
		"ORBIT_CLIENT_GATEWAY_ADDRESS="+gatewayAddress,
		"ORBIT_CLIENT_STATE_PATH="+statePath,
		"ORBIT_CLIENT_RECONNECT_INITIAL_DELAY=50ms",
		"ORBIT_CLIENT_RECONNECT_MAX_DELAY=200ms",
	))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	prepareInterruptible(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(statePath); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatal("client never persisted a device session")
	}
	if err := interrupt(cmd); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitExit(t, cmd, 8*time.Second)
}
