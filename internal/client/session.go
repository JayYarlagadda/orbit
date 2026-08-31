package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/metrics"
	"github.com/JayYarlagadda/orbit/internal/session"
	"github.com/JayYarlagadda/orbit/internal/telemetry"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SessionConfig struct {
	DeviceID         string
	ClientInstanceID string
	Heartbeat        heartbeat.Settings
}

type sessionReceiveResult struct {
	frame *orbitv1.ServerFrame
	err   error
}

func RunSession(
	ctx context.Context,
	client orbitv1.DeviceServiceClient,
	config SessionConfig,
	state *StateStore,
	handler Handler,
) error {
	ctx, span := telemetry.Start(ctx, "orbit.client.session", telemetry.DeviceID(config.DeviceID))
	var runErr error
	defer func() {
		metrics.SetClientSessionActive(false)
		telemetry.End(span, runErr)
	}()

	deviceID, err := session.NormalizeIdentifier("device_id", config.DeviceID)
	if err != nil {
		return err
	}
	clientInstanceID, err := session.NormalizeIdentifier("client_instance_id", config.ClientInstanceID)
	if err != nil {
		return err
	}
	stream, err := client.Connect(ctx)
	if err != nil {
		return fmt.Errorf("open device stream: %w", err)
	}
	if err := stream.Send(&orbitv1.DeviceFrame{
		Body: &orbitv1.DeviceFrame_Hello{Hello: &orbitv1.DeviceHello{
			DeviceId:                 deviceID,
			ClientInstanceId:         clientInstanceID,
			LastObservedSessionEpoch: state.LastSessionEpoch(),
			LastSeenSequence:         state.LastSeenSequence(),
		}},
	}); err != nil {
		return fmt.Errorf("send device hello: %w", err)
	}
	first, err := stream.Recv()
	if err != nil {
		return fmt.Errorf("receive device session: %w", err)
	}
	opened := first.GetSessionOpened()
	if opened == nil || opened.DeviceId != deviceID || opened.SessionEpoch < 1 {
		return errors.New("gateway returned an invalid device session")
	}
	if err := state.ObserveSession(opened.SessionEpoch); err != nil {
		runErr = fmt.Errorf("persist device session: %w", err)
		return runErr
	}
	metrics.SetClientSessionActive(true)

	settings := config.Heartbeat
	if settings.Interval == 0 {
		settings = heartbeat.Default()
	}
	if err := settings.Validate(); err != nil {
		return err
	}
	watch := heartbeat.NewWatch(settings.Timeout)
	watchCtx, stopWatch := heartbeat.WatchContext(ctx, watch)
	defer stopWatch()

	received := make(chan sessionReceiveResult, 1)
	go receiveSessionFrames(watchCtx, stream, received)
	ticker := time.NewTicker(settings.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-watchCtx.Done():
			return fmt.Errorf("device session: %w", context.Cause(watchCtx))
		case <-ticker.C:
			if err := stream.Send(&orbitv1.DeviceFrame{Body: &orbitv1.DeviceFrame_Heartbeat{Heartbeat: &orbitv1.Heartbeat{}}}); err != nil {
				return fmt.Errorf("send heartbeat: %w", err)
			}
		case result := <-received:
			watch.Touch()
			if errors.Is(result.err, io.EOF) {
				return nil
			}
			if result.err != nil {
				return fmt.Errorf("receive device frame: %w", result.err)
			}
			if result.frame.GetHeartbeat() != nil {
				continue
			}
			delivery := result.frame.GetCommand()
			if delivery == nil {
				return errors.New("gateway sent an unsupported device frame")
			}
			if delivery.DeviceId != deviceID || delivery.SessionEpoch != opened.SessionEpoch {
				return errors.New("command delivery does not match the active session")
			}
			resultHash, _, err := state.Apply(ctx, delivery, handler)
			if err != nil {
				return err
			}
			if err := stream.Send(&orbitv1.DeviceFrame{
				Body: &orbitv1.DeviceFrame_Ack{Ack: &orbitv1.CommandAck{
					CommandId:       delivery.CommandId,
					DeviceId:        delivery.DeviceId,
					SequenceNumber:  delivery.SequenceNumber,
					LeaseToken:      delivery.LeaseToken,
					SessionEpoch:    delivery.SessionEpoch,
					ResultHash:      resultHash[:],
					ClientAppliedAt: timestamppb.New(time.Now().UTC()),
				}},
			}); err != nil {
				return fmt.Errorf("send command acknowledgement: %w", err)
			}
		}
	}
}

func receiveSessionFrames(
	ctx context.Context,
	stream orbitv1.DeviceService_ConnectClient,
	results chan<- sessionReceiveResult,
) {
	for {
		frame, err := stream.Recv()
		select {
		case results <- sessionReceiveResult{frame: frame, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}
