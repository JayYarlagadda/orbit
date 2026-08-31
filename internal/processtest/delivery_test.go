package processtest

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc"
)

type duplicateControlServer struct {
	orbitv1.UnimplementedGatewayControlServiceServer
	acks chan *orbitv1.CommandAck
}

func (s *duplicateControlServer) Connect(stream orbitv1.GatewayControlService_ConnectServer) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	onlineFrame, err := stream.Recv()
	if err != nil {
		return err
	}
	online := onlineFrame.GetDeviceOnline()
	if online == nil {
		return fmt.Errorf("expected DeviceOnline after hello")
	}
	if err := stream.Send(&orbitv1.ControlFrame{Body: &orbitv1.ControlFrame_DeviceSession{
		DeviceSession: &orbitv1.DeviceSession{
			ConnectionId: online.ConnectionId,
			DeviceId:     online.DeviceId,
			SessionEpoch: 1,
		},
	}}); err != nil {
		return err
	}
	payload := []byte("collect-diagnostics")
	hash := sha256.Sum256(payload)
	delivery := &orbitv1.CommandDelivery{
		CommandId:      "command-dup-1",
		DeviceId:       online.DeviceId,
		SequenceNumber: 1,
		Payload:        payload,
		PayloadHash:    hash[:],
		LeaseToken:     1,
		SessionEpoch:   1,
	}
	assignment := &orbitv1.ControlFrame{Body: &orbitv1.ControlFrame_CommandAssignment{
		CommandAssignment: &orbitv1.GatewayCommandAssignment{
			ConnectionId: online.ConnectionId,
			Command:      delivery,
		},
	}}
	if err := stream.Send(assignment); err != nil {
		return err
	}
	if err := stream.Send(assignment); err != nil {
		return err
	}
	for {
		frame, err := stream.Recv()
		if err != nil {
			return err
		}
		if ack := frame.GetCommandAck(); ack != nil && ack.Ack != nil {
			select {
			case s.acks <- ack.Ack:
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
	}
}

type lostAckDeviceServer struct {
	orbitv1.UnimplementedDeviceServiceServer
	connects atomic.Int32
	second   chan *orbitv1.CommandAck
}

func (s *lostAckDeviceServer) Connect(stream orbitv1.DeviceService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return fmt.Errorf("first device frame must be hello")
	}
	epoch := int64(s.connects.Add(1))
	if err := stream.Send(&orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_SessionOpened{
		SessionOpened: &orbitv1.SessionOpened{DeviceId: hello.DeviceId, SessionEpoch: epoch},
	}}); err != nil {
		return err
	}
	payload := []byte("collect-diagnostics")
	hash := sha256.Sum256(payload)
	if err := stream.Send(&orbitv1.ServerFrame{Body: &orbitv1.ServerFrame_Command{Command: &orbitv1.CommandDelivery{
		CommandId:      "command-lost-ack",
		DeviceId:       hello.DeviceId,
		SequenceNumber: 1,
		Payload:        payload,
		PayloadHash:    hash[:],
		LeaseToken:     epoch,
		SessionEpoch:   epoch,
	}}}); err != nil {
		return err
	}
	if epoch == 1 {
		return nil
	}
	ackFrame, err := stream.Recv()
	if err != nil {
		return err
	}
	if ack := ackFrame.GetAck(); ack != nil {
		s.second <- ack
	}
	<-stream.Context().Done()
	return nil
}

func TestGatewayClientProcessDedupsDuplicateDelivery(t *testing.T) {
	control := &duplicateControlServer{acks: make(chan *orbitv1.CommandAck, 2)}
	controlAddress := serveGRPC(t, func(server *grpc.Server) {
		orbitv1.RegisterGatewayControlServiceServer(server, control)
	})
	listenAddress := freeAddress(t)
	statePath := filepath.Join(t.TempDir(), "state.json")

	gatewayCmd := exec.Command(gatewayBinary)
	gatewayCmd.Env = withTelemetryOff(append(os.Environ(),
		"ORBIT_GATEWAY_ID=gateway-dup",
		"ORBIT_CONTROL_ADDRESS="+controlAddress,
		"ORBIT_GATEWAY_LISTEN_ADDRESS="+listenAddress,
		"ORBIT_GATEWAY_SHUTDOWN_TIMEOUT=2s",
	))
	gatewayCmd.Stdout = os.Stdout
	gatewayCmd.Stderr = os.Stderr
	prepareInterruptible(gatewayCmd)
	if err := gatewayCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = gatewayCmd.Process.Kill() })
	waitHealthy(t, listenAddress, 10*time.Second)

	var clientLog logBuffer
	clientCmd := exec.Command(clientBinary)
	clientCmd.Env = withTelemetryOff(append(os.Environ(),
		"ORBIT_DEVICE_ID=edge-dup",
		"ORBIT_CLIENT_GATEWAY_ADDRESS="+listenAddress,
		"ORBIT_CLIENT_STATE_PATH="+statePath,
	))
	clientCmd.Stdout = &clientLog
	clientCmd.Stderr = os.Stderr
	prepareInterruptible(clientCmd)
	if err := clientCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientCmd.Process.Kill() })

	acks := make([]*orbitv1.CommandAck, 0, 2)
	deadline := time.Now().Add(10 * time.Second)
	for len(acks) < 2 && time.Now().Before(deadline) {
		select {
		case ack := <-control.acks:
			acks = append(acks, ack)
		case <-time.After(100 * time.Millisecond):
		}
	}
	if len(acks) != 2 {
		t.Fatalf("acks = %d, want 2\nclient log:\n%s", len(acks), clientLog.String())
	}
	if acks[0].CommandId != "command-dup-1" || acks[1].CommandId != "command-dup-1" {
		t.Fatalf("acks = %+v", acks)
	}
	if string(acks[0].ResultHash) != string(acks[1].ResultHash) {
		t.Fatal("duplicate ACKs used different result hashes")
	}
	if got := bytes.Count(clientLog.Bytes(), []byte(`"msg":"applying command"`)); got != 1 {
		t.Fatalf("handler invocations = %d, want 1\n%s", got, clientLog.String())
	}
	if seq := lastSeenSequence(t, statePath); seq != 1 {
		t.Fatalf("last_seen_sequence = %d, want 1", seq)
	}

	_ = interrupt(clientCmd)
	_ = interrupt(gatewayCmd)
	waitExit(t, clientCmd, 8*time.Second)
	waitExit(t, gatewayCmd, 8*time.Second)
}

func TestClientProcessDoesNotReapplyAfterLostAck(t *testing.T) {
	device := &lostAckDeviceServer{second: make(chan *orbitv1.CommandAck, 1)}
	gatewayAddress := serveGRPC(t, func(server *grpc.Server) {
		orbitv1.RegisterDeviceServiceServer(server, device)
	})
	statePath := filepath.Join(t.TempDir(), "state.json")
	var clientLog logBuffer
	clientCmd := exec.Command(clientBinary)
	clientCmd.Env = append(os.Environ(),
		"ORBIT_DEVICE_ID=edge-lost-ack",
		"ORBIT_CLIENT_GATEWAY_ADDRESS="+gatewayAddress,
		"ORBIT_CLIENT_STATE_PATH="+statePath,
		"ORBIT_CLIENT_RECONNECT_INITIAL_DELAY=50ms",
		"ORBIT_CLIENT_RECONNECT_MAX_DELAY=200ms",
	)
	clientCmd.Stdout = &clientLog
	clientCmd.Stderr = os.Stderr
	prepareInterruptible(clientCmd)
	if err := clientCmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = clientCmd.Process.Kill() })

	select {
	case ack := <-device.second:
		if ack.CommandId != "command-lost-ack" {
			t.Fatalf("second ACK = %+v", ack)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("timed out waiting for the replay ACK\n%s", clientLog.String())
	}
	if got := bytes.Count(clientLog.Bytes(), []byte(`"msg":"applying command"`)); got != 1 {
		t.Fatalf("handler invocations = %d, want 1\n%s", got, clientLog.String())
	}
	if seq := lastSeenSequence(t, statePath); seq != 1 {
		t.Fatalf("last_seen_sequence = %d, want 1", seq)
	}

	if err := interrupt(clientCmd); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitExit(t, clientCmd, 8*time.Second)
}

func lastSeenSequence(t *testing.T, path string) int64 {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state struct {
		LastSeenSequence int64 `json:"last_seen_sequence"`
	}
	if err := json.Unmarshal(contents, &state); err != nil {
		t.Fatal(err)
	}
	return state.LastSeenSequence
}
