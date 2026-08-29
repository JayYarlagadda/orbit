package gateway

import (
	"context"
	"io"
	"log/slog"
	"runtime"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
)

// Soak bounds: 25 control-stream drop/reconnect cycles plus concurrent device
// connect attempts. Steady-state goroutine drift must stay within 24 of the
// post-warmup snapshot, and heap growth within 16 MiB. This is a leak check,
// not a published benchmark.
const (
	soakControlCycles  = 25
	soakWarmupCycles   = 8
	soakDeviceAttempts = 30
	soakGoroutineSlack = 24
	soakHeapSlackBytes = 16 << 20
	soakControlHold    = 15 * time.Millisecond
	soakReconnectDelay = 5 * time.Millisecond
)

type cyclingControlServer struct {
	orbitv1.UnimplementedGatewayControlServiceServer
	hold time.Duration
	closingControlServer
}

func (s *cyclingControlServer) Connect(stream orbitv1.GatewayControlService_ConnectServer) error {
	s.connects.Add(1)
	if _, err := stream.Recv(); err != nil {
		return err
	}
	timer := time.NewTimer(s.hold)
	defer timer.Stop()
	select {
	case <-stream.Context().Done():
		return stream.Context().Err()
	case <-timer.C:
		return nil
	}
}

type resourceSnapshot struct {
	goroutines int
	heap       uint64
}

func snapshotResources() resourceSnapshot {
	runtime.GC()
	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return resourceSnapshot{goroutines: runtime.NumGoroutine(), heap: stats.HeapAlloc}
}

func waitForConnects(t *testing.T, server *cyclingControlServer, want int32, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if server.connects.Load() >= want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("control connects = %d, want at least %d", server.connects.Load(), want)
}

func TestRunControlReconnectSoak(t *testing.T) {
	control := &cyclingControlServer{hold: soakControlHold}
	controlClient, stopControl := startControlLoop(t, control)
	defer stopControl()
	hub := newTestHub(t, 8)
	deviceClient, stopDevice := startDeviceLoop(t, hub)
	defer stopDevice()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlDone := make(chan error, 1)
	go func() {
		controlDone <- RunControl(ctx, controlClient, hub, ControlConfig{
			GatewayInstanceID: "instance-soak",
			InitialDelay:      soakReconnectDelay,
			MaxDelay:          soakReconnectDelay,
			Logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()

	deviceDone := make(chan struct{})
	go func() {
		defer close(deviceDone)
		hello := &orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Hello{
			Hello: &orbitv1.DeviceHello{DeviceId: "device-1", ClientInstanceId: "client-soak"},
		}}
		for range soakDeviceAttempts {
			if ctx.Err() != nil {
				return
			}
			attempt, stopAttempt := context.WithTimeout(ctx, 150*time.Millisecond)
			stream, err := deviceClient.Connect(attempt)
			if err != nil {
				stopAttempt()
				continue
			}
			_ = stream.Send(hello)
			_, _ = stream.Recv()
			stopAttempt()
		}
	}()

	waitForConnects(t, control, soakWarmupCycles, 5*time.Second)
	warmup := snapshotResources()
	waitForConnects(t, control, soakControlCycles, 8*time.Second)
	<-deviceDone
	steady := snapshotResources()

	if steady.goroutines > warmup.goroutines+soakGoroutineSlack {
		t.Fatalf("goroutines grew from %d to %d, slack %d", warmup.goroutines, steady.goroutines, soakGoroutineSlack)
	}
	if steady.heap > warmup.heap+soakHeapSlackBytes {
		t.Fatalf("heap grew from %d to %d bytes, slack %d", warmup.heap, steady.heap, soakHeapSlackBytes)
	}

	cancel()
	select {
	case <-controlDone:
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for RunControl to return")
	}
}
