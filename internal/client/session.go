package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/session"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type SessionConfig struct {
	DeviceID         string
	ClientInstanceID string
}

func RunSession(
	ctx context.Context,
	client orbitv1.DeviceServiceClient,
	config SessionConfig,
	state *StateStore,
	handler Handler,
) error {
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
		return fmt.Errorf("persist device session: %w", err)
	}

	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("receive device frame: %w", err)
		}
		delivery := frame.GetCommand()
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
