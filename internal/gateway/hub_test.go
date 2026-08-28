package gateway

import (
	"context"
	"errors"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
)

const testTimeout = 2 * time.Second

type registerResult struct {
	connection *Connection
	err        error
}

func newTestHub(t *testing.T, connectionBuffer int) *Hub {
	t.Helper()
	hub, err := NewHub(HubConfig{
		GatewayID:        "gateway-1",
		ControlBuffer:    16,
		ConnectionBuffer: connectionBuffer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return hub
}

// startRegister begins a registration and waits for the DeviceOnline frame.
// Register publishes the connection before sending that frame, so observing it
// guarantees the connection is routable and later Deliver calls cannot be
// silently dropped as unknown.
func startRegister(t *testing.T, ctx context.Context, hub *Hub, connectionID, deviceID string) <-chan registerResult {
	t.Helper()
	results := make(chan registerResult, 1)
	go func() {
		connection, err := hub.Register(ctx, connectionID, deviceID, "client-1")
		results <- registerResult{connection: connection, err: err}
	}()
	select {
	case frame := <-hub.Outbound():
		online := frame.GetDeviceOnline()
		if online == nil || online.ConnectionId != connectionID || online.DeviceId != deviceID {
			t.Fatalf("first outbound frame = %+v, want DeviceOnline for %q", frame, connectionID)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the DeviceOnline frame")
	}
	return results
}

func awaitRegister(t *testing.T, results <-chan registerResult) registerResult {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for Register to return")
		return registerResult{}
	}
}

func sessionFrame(connectionID, deviceID string, epoch int64) *orbitv1.ControlFrame {
	return &orbitv1.ControlFrame{Body: &orbitv1.ControlFrame_DeviceSession{
		DeviceSession: &orbitv1.DeviceSession{
			ConnectionId: connectionID,
			DeviceId:     deviceID,
			SessionEpoch: epoch,
		},
	}}
}

func assignmentFrame(connectionID, commandID string) *orbitv1.ControlFrame {
	return &orbitv1.ControlFrame{Body: &orbitv1.ControlFrame_CommandAssignment{
		CommandAssignment: &orbitv1.GatewayCommandAssignment{
			ConnectionId: connectionID,
			Command:      &orbitv1.CommandDelivery{CommandId: commandID},
		},
	}}
}

func TestRegisterOpensSessionFromControlPlane(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	if err := hub.Deliver(ctx, sessionFrame("connection-1", "device-1", 7)); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	result := awaitRegister(t, results)
	if result.err != nil {
		t.Fatalf("Register() error = %v", result.err)
	}
	if result.connection.SessionEpoch != 7 {
		t.Fatalf("SessionEpoch = %d, want 7", result.connection.SessionEpoch)
	}
}

func TestRegisterReplaysFramesQueuedBeforeSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	for _, commandID := range []string{"command-1", "command-2"} {
		if err := hub.Deliver(ctx, assignmentFrame("connection-1", commandID)); err != nil {
			t.Fatalf("Deliver(%s) error = %v", commandID, err)
		}
	}
	if err := hub.Deliver(ctx, sessionFrame("connection-1", "device-1", 2)); err != nil {
		t.Fatalf("Deliver(session) error = %v", err)
	}
	result := awaitRegister(t, results)
	if result.err != nil {
		t.Fatalf("Register() error = %v", result.err)
	}

	for _, want := range []string{"command-1", "command-2"} {
		select {
		case frame := <-result.connection.Frames():
			if got := frame.GetCommandAssignment().Command.CommandId; got != want {
				t.Fatalf("replayed command = %q, want %q", got, want)
			}
		case <-time.After(testTimeout):
			t.Fatalf("timed out waiting for replayed frame %q", want)
		}
	}
}

// A pre-session backlog larger than the connection buffer used to deadlock:
// Register replayed it with a blocking send while it was the only reader.
func TestRegisterRejectsBacklogLargerThanConnectionBuffer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 2)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	go func() {
		for _, commandID := range []string{"command-1", "command-2", "command-3"} {
			if err := hub.Deliver(ctx, assignmentFrame("connection-1", commandID)); err != nil {
				return
			}
		}
	}()

	result := awaitRegister(t, results)
	if result.err == nil {
		t.Fatal("Register() accepted an unbounded pre-session backlog")
	}
	if result.connection != nil {
		t.Fatalf("Register() returned a connection alongside an error: %+v", result.connection)
	}
	// The failed registration must not leave the connection routable.
	if err := hub.Deliver(ctx, assignmentFrame("connection-1", "command-4")); err != nil {
		t.Fatalf("Deliver() after failed registration error = %v", err)
	}
}

func TestRegisterRejectsMismatchedDeviceSession(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	if err := hub.Deliver(ctx, sessionFrame("connection-1", "device-2", 1)); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if result := awaitRegister(t, results); result.err == nil {
		t.Fatal("Register() accepted a session for a different device")
	}
}

func TestRegisterRejectsNonPositiveSessionEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	if err := hub.Deliver(ctx, sessionFrame("connection-1", "device-1", 0)); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if result := awaitRegister(t, results); result.err == nil {
		t.Fatal("Register() accepted a zero session epoch")
	}
}

func TestRegisterRejectsDuplicateConnectionID(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")
	defer func() {
		_ = hub.Deliver(ctx, sessionFrame("connection-1", "device-1", 1))
		awaitRegister(t, results)
	}()

	if _, err := hub.Register(ctx, "connection-1", "device-1", "client-2"); err == nil {
		t.Fatal("Register() accepted a duplicate connection ID")
	}
}

func TestRegisterStopsWhenControlStreamFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")

	hub.Fail()
	result := awaitRegister(t, results)
	if !errors.Is(result.err, ErrControlDisconnected) {
		t.Fatalf("Register() error = %v, want ErrControlDisconnected", result.err)
	}
}

func TestDeliverIgnoresUnknownConnection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)

	if err := hub.Deliver(ctx, assignmentFrame("connection-missing", "command-1")); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
}

func TestDeliverRejectsUnsupportedFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)

	if err := hub.Deliver(ctx, &orbitv1.ControlFrame{}); err == nil {
		t.Fatal("Deliver() accepted a frame with no body")
	}
}

func TestUnregisterReportsDeviceOffline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	hub := newTestHub(t, 4)
	results := startRegister(t, ctx, hub, "connection-1", "device-1")
	if err := hub.Deliver(ctx, sessionFrame("connection-1", "device-1", 5)); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	result := awaitRegister(t, results)
	if result.err != nil {
		t.Fatalf("Register() error = %v", result.err)
	}

	if err := hub.Unregister(ctx, result.connection); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}
	select {
	case frame := <-hub.Outbound():
		offline := frame.GetDeviceOffline()
		if offline == nil || offline.ConnectionId != "connection-1" || offline.SessionEpoch != 5 {
			t.Fatalf("outbound frame = %+v, want DeviceOffline with epoch 5", frame)
		}
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the DeviceOffline frame")
	}

	// The device must no longer be routable once it is unregistered.
	if err := hub.Deliver(ctx, assignmentFrame("connection-1", "command-1")); err != nil {
		t.Fatalf("Deliver() after Unregister error = %v", err)
	}
}

func TestFailIsIdempotent(t *testing.T) {
	hub := newTestHub(t, 4)
	hub.Fail()
	hub.Fail()
	select {
	case <-hub.Done():
	default:
		t.Fatal("Done() was not closed after Fail()")
	}
}
