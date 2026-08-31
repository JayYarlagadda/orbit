package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/backoff"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/metrics"
	"github.com/JayYarlagadda/orbit/internal/telemetry"
)

// healthyControlStream is how long a control stream must last before the
// reconnect backoff is treated as recovered and reset to its initial delay.
const healthyControlStream = 30 * time.Second

// ControlConfig bounds how a gateway reattaches to the control plane.
type ControlConfig struct {
	GatewayInstanceID string
	InitialDelay      time.Duration
	MaxDelay          time.Duration
	// MaxAttempts limits consecutive reconnect attempts. Zero means the
	// gateway retries for as long as it runs; the rate stays bounded by
	// MaxDelay either way.
	MaxAttempts int
	Heartbeat   heartbeat.Settings
	Logger      *slog.Logger
}

// RunControl keeps a gateway attached to the control plane for as long as ctx
// lives. A gateway used to exit when its control stream failed, so restarting
// orbitd took every gateway down with it; each failure is now retried with
// bounded, jittered backoff instead.
func RunControl(
	ctx context.Context,
	client orbitv1.GatewayControlServiceClient,
	hub *Hub,
	config ControlConfig,
) error {
	if client == nil || hub == nil {
		return errors.New("gateway control stream requires a client and a hub")
	}
	if config.Logger == nil {
		return errors.New("gateway control stream requires a logger")
	}
	if config.Heartbeat.Interval == 0 {
		config.Heartbeat = heartbeat.Default()
	}
	if err := config.Heartbeat.Validate(); err != nil {
		return err
	}
	policy, err := backoff.New(config.InitialDelay, config.MaxDelay)
	if err != nil {
		return err
	}

	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			metrics.RecordGatewayControlReconnect()
			reconnectCtx, reconnectSpan := telemetry.Start(
				ctx,
				"orbit.gateway.control.reconnect",
				telemetry.ReconnectAttempt(attempt),
			)
			_ = reconnectCtx
			telemetry.End(reconnectSpan, nil)
			// Drop the previous stream's device sessions before opening a new
			// one, so devices re-register for epochs the control plane knows.
			hub.Rebind()
		}
		startedAt := time.Now()
		streamCtx, streamSpan := telemetry.Start(ctx, "orbit.gateway.control.stream")
		streamErr := RunControlStream(streamCtx, client, hub, config.GatewayInstanceID, config.Heartbeat)
		telemetry.End(streamSpan, streamErr)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(streamErr, ErrControlDisconnected) {
			config.Logger.Warn("control stream closed by the control plane")
		} else {
			config.Logger.Warn("control stream ended", "error", streamErr)
		}

		// A stream that stayed up is evidence the control plane recovered, so
		// the next outage restarts from the initial delay rather than the cap.
		if time.Since(startedAt) >= healthyControlStream {
			policy.Reset()
		}
		if config.MaxAttempts > 0 && policy.Attempts() >= config.MaxAttempts {
			return fmt.Errorf(
				"gateway control stream failed %d consecutive times: %w",
				policy.Attempts()+1,
				streamErr,
			)
		}
		wait := policy.Next()
		config.Logger.Info("reconnecting to the control plane",
			"attempt", policy.Attempts(),
			"delay", wait.String(),
		)
		if err := backoff.Wait(ctx, wait); err != nil {
			return err
		}
	}
}

// RunControlStream serves one control stream and returns when it ends. It
// always fails the hub before returning, so device sessions opened against this
// stream are torn down rather than left unroutable.
func RunControlStream(
	ctx context.Context,
	client orbitv1.GatewayControlServiceClient,
	hub *Hub,
	gatewayInstanceID string,
	settings heartbeat.Settings,
) error {
	if settings.Interval == 0 {
		settings = heartbeat.Default()
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	// Cancelling this context on return aborts the RPC, which is what unblocks
	// the receive goroutine and stops the send goroutine from consuming
	// outbound frames that a later stream would need to deliver.
	streamContext, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	watch := heartbeat.NewWatch(settings.Timeout)
	watchCtx, stopWatch := heartbeat.WatchContext(streamContext, watch)
	defer stopWatch()

	stream, err := client.Connect(watchCtx)
	if err != nil {
		return fmt.Errorf("open gateway control stream: %w", err)
	}
	if err := stream.Send(&orbitv1.GatewayFrame{
		Body: &orbitv1.GatewayFrame_Hello{Hello: &orbitv1.GatewayHello{
			GatewayId:         hub.gatewayID,
			GatewayInstanceId: gatewayInstanceID,
		}},
	}); err != nil {
		return fmt.Errorf("send gateway hello: %w", err)
	}

	defer hub.Fail()
	ticker := time.NewTicker(settings.Interval)
	defer ticker.Stop()
	var workers sync.WaitGroup
	sendErrors := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case <-watchCtx.Done():
				return
			case <-ticker.C:
				if err := stream.Send(&orbitv1.GatewayFrame{Body: &orbitv1.GatewayFrame_Heartbeat{Heartbeat: &orbitv1.Heartbeat{}}}); err != nil {
					sendErrors <- err
					return
				}
			case frame := <-hub.Outbound():
				if err := stream.Send(frame); err != nil {
					sendErrors <- err
					return
				}
			}
		}
	}()
	receiveErrors := make(chan error, 1)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			frame, err := stream.Recv()
			if err != nil {
				receiveErrors <- err
				return
			}
			watch.Touch()
			if frame.GetHeartbeat() != nil {
				continue
			}
			if err := hub.Deliver(watchCtx, frame); err != nil {
				receiveErrors <- err
				return
			}
		}
	}()

	var runError error
	select {
	case <-watchCtx.Done():
		_ = stream.CloseSend()
		runError = context.Cause(watchCtx)
	case err := <-sendErrors:
		runError = fmt.Errorf("gateway control send: %w", err)
	case err := <-receiveErrors:
		if errors.Is(err, io.EOF) {
			runError = ErrControlDisconnected
		} else {
			runError = fmt.Errorf("gateway control receive: %w", err)
		}
	}

	// Each worker sends at most one error into its own buffered channel, so
	// neither can block here even though only one of them was read above.
	cancelStream()
	workers.Wait()
	return runError
}
