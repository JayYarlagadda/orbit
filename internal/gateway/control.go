package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
)

func RunControlStream(
	ctx context.Context,
	client orbitv1.GatewayControlServiceClient,
	hub *Hub,
	gatewayInstanceID string,
) error {
	stream, err := client.Connect(ctx)
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
	sendErrors := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				sendErrors <- ctx.Err()
				return
			case frame := <-hub.Outbound():
				if err := stream.Send(frame); err != nil {
					sendErrors <- err
					return
				}
			}
		}
	}()
	receiveErrors := make(chan error, 1)
	go func() {
		for {
			frame, err := stream.Recv()
			if err != nil {
				receiveErrors <- err
				return
			}
			if err := hub.Deliver(ctx, frame); err != nil {
				receiveErrors <- err
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		_ = stream.CloseSend()
		return ctx.Err()
	case err := <-sendErrors:
		return fmt.Errorf("gateway control send: %w", err)
	case err := <-receiveErrors:
		if errors.Is(err, io.EOF) {
			return ErrControlDisconnected
		}
		return fmt.Errorf("gateway control receive: %w", err)
	}
}
