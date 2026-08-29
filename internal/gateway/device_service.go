package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type DeviceService struct {
	orbitv1.UnimplementedDeviceServiceServer
	hub *Hub
}

func NewDeviceService(hub *Hub) *DeviceService { return &DeviceService{hub: hub} }

func (s *DeviceService) Connect(stream orbitv1.DeviceService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return deviceStreamError(err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first device frame must be hello")
	}
	connectionID, err := newConnectionID()
	if err != nil {
		return status.Error(codes.Internal, "could not create connection ID")
	}
	connection, err := s.hub.Register(stream.Context(), connectionID, hello.DeviceId, hello.ClientInstanceId)
	if err != nil {
		return deviceStreamError(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), disconnectTimeout)
		defer cancel()
		_ = s.hub.Unregister(ctx, connection)
	}()

	if err := stream.Send(&orbitv1.ServerFrame{
		Body: &orbitv1.ServerFrame_SessionOpened{SessionOpened: &orbitv1.SessionOpened{
			DeviceId:     connection.DeviceID,
			SessionEpoch: connection.SessionEpoch,
		}},
	}); err != nil {
		return deviceStreamError(err)
	}

	received := make(chan deviceReceiveResult, 1)
	go receiveDeviceFrames(stream.Context(), stream, received)
	for {
		select {
		case <-stream.Context().Done():
			return deviceStreamError(stream.Context().Err())
		case <-connection.Disconnected():
			// The control plane released this session, so nothing can be
			// routed to it any more. Ending the stream makes the device
			// reconnect and register for a fresh epoch.
			return deviceStreamError(ErrControlDisconnected)
		case result := <-received:
			if connection.ended() {
				return deviceStreamError(ErrControlDisconnected)
			}
			if result.err != nil {
				return deviceStreamError(result.err)
			}
			ack := result.frame.GetAck()
			if ack == nil {
				return status.Error(codes.InvalidArgument, "device stream accepts only ACK frames after hello")
			}
			if ack.DeviceId != connection.DeviceID || ack.SessionEpoch != connection.SessionEpoch {
				return status.Error(codes.FailedPrecondition, "ACK does not match the active device session")
			}
			if err := s.hub.ReportAcknowledgement(stream.Context(), ack); err != nil {
				return deviceStreamError(err)
			}
		case controlFrame := <-connection.Frames():
			if connection.ended() {
				return deviceStreamError(ErrControlDisconnected)
			}
			assignment := controlFrame.GetCommandAssignment()
			if assignment == nil || assignment.Command == nil {
				return status.Error(codes.Internal, "gateway received invalid command assignment")
			}
			delivery := assignment.Command
			if delivery.DeviceId != connection.DeviceID || delivery.SessionEpoch != connection.SessionEpoch {
				return status.Error(codes.FailedPrecondition, "assignment does not match the active device session")
			}
			if err := stream.Send(&orbitv1.ServerFrame{
				Body: &orbitv1.ServerFrame_Command{Command: delivery},
			}); err != nil {
				return deviceStreamError(err)
			}
			if err := s.hub.ReportDeliveryStarted(stream.Context(), delivery); err != nil {
				return deviceStreamError(err)
			}
		}
	}
}

const disconnectTimeout = 3 * time.Second

type deviceReceiveResult struct {
	frame *orbitv1.DeviceFrame
	err   error
}

func receiveDeviceFrames(
	ctx context.Context,
	stream orbitv1.DeviceService_ConnectServer,
	results chan<- deviceReceiveResult,
) {
	for {
		frame, err := stream.Recv()
		select {
		case results <- deviceReceiveResult{frame: frame, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func newConnectionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func deviceStreamError(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "device stream canceled")
	case errors.Is(err, ErrControlDisconnected):
		return status.Error(codes.Unavailable, ErrControlDisconnected.Error())
	default:
		return err
	}
}
