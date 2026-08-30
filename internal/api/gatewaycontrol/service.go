package gatewaycontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/command"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"github.com/JayYarlagadda/orbit/internal/scheduler"
	"github.com/JayYarlagadda/orbit/internal/session"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Store interface {
	scheduler.Store
	AcquireSession(context.Context, session.Acquisition) (session.Session, error)
	ReleaseSession(context.Context, string, string, int64) error
	MarkInFlight(context.Context, string, string, int64, int64, string) (command.Command, error)
	Acknowledge(context.Context, command.Acknowledgement, string) (command.Command, error)
}

type Config struct {
	OutboundBuffer int
	BatchSize      int
	SweepLimit     int
	LeaseDuration  time.Duration
	PollInterval   time.Duration
	Heartbeat      heartbeat.Settings
}

type Service struct {
	orbitv1.UnimplementedGatewayControlServiceServer
	store  Store
	config Config
}

func New(store Store, config Config) (*Service, error) {
	if store == nil {
		return nil, errors.New("gateway control store is required")
	}
	if config.OutboundBuffer < 1 || config.OutboundBuffer > 4096 {
		return nil, errors.New("gateway outbound buffer must be between 1 and 4096")
	}
	return &Service{store: store, config: config}, nil
}

func (s *Service) Connect(stream orbitv1.GatewayControlService_ConnectServer) error {
	first, err := stream.Recv()
	if err != nil {
		return mapStreamError(err)
	}
	hello := first.GetHello()
	if hello == nil {
		return status.Error(codes.InvalidArgument, "first gateway frame must be hello")
	}
	gatewayID, err := session.NormalizeIdentifier("gateway_id", hello.GatewayId)
	if err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}
	if _, err := session.NormalizeIdentifier("gateway_instance_id", hello.GatewayInstanceId); err != nil {
		return status.Error(codes.InvalidArgument, err.Error())
	}

	settings := s.config.Heartbeat
	if settings.Interval == 0 {
		settings = heartbeat.Default()
	}
	if err := settings.Validate(); err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	watch := heartbeat.NewWatch(settings.Timeout)
	watchCtx, stopWatch := heartbeat.WatchContext(stream.Context(), watch)
	defer stopWatch()

	ctx, cancel := context.WithCancel(watchCtx)
	defer cancel()
	outbound := make(chan *orbitv1.ControlFrame, s.config.OutboundBuffer)
	routes := newRouteTable()
	dispatcher := &streamDispatcher{ctx: ctx, outbound: outbound, routes: routes}
	commandScheduler, err := scheduler.New(
		s.store,
		dispatcher,
		scheduler.Config{
			GatewayID:     gatewayID,
			BatchSize:     s.config.BatchSize,
			LeaseDuration: s.config.LeaseDuration,
			PollInterval:  s.config.PollInterval,
			SweepLimit:    s.config.SweepLimit,
		},
		nil,
		nil,
	)
	if err != nil {
		return status.Error(codes.Internal, "invalid server scheduler configuration")
	}

	sendErrors := make(chan error, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				sendErrors <- ctx.Err()
				return
			case frame := <-outbound:
				if err := stream.Send(frame); err != nil {
					sendErrors <- err
					return
				}
			}
		}
	}()

	received := make(chan receiveResult, 1)
	go receiveFrames(watchCtx, stream, received)
	schedulerErrors := make(chan error, 1)
	go commandScheduler.Run(ctx, func(err error) {
		select {
		case schedulerErrors <- err:
		default:
		}
	})

	ticker := time.NewTicker(settings.Interval)
	defer ticker.Stop()
	defer s.releaseRoutes(gatewayID, routes)
	for {
		select {
		case <-watchCtx.Done():
			return mapStreamError(context.Cause(watchCtx))
		case err := <-sendErrors:
			return mapStreamError(err)
		case err := <-schedulerErrors:
			return status.Error(codes.Unavailable, err.Error())
		case <-ticker.C:
			if err := stream.Send(&orbitv1.ControlFrame{Body: &orbitv1.ControlFrame_Heartbeat{Heartbeat: &orbitv1.Heartbeat{}}}); err != nil {
				return mapStreamError(err)
			}
		case result := <-received:
			if result.err != nil {
				return mapStreamError(result.err)
			}
			watch.Touch()
			if result.frame.GetHeartbeat() != nil {
				continue
			}
			if err := s.handleFrame(ctx, gatewayID, routes, outbound, result.frame); err != nil {
				return err
			}
		}
	}
}

type receiveResult struct {
	frame *orbitv1.GatewayFrame
	err   error
}

func receiveFrames(
	ctx context.Context,
	stream orbitv1.GatewayControlService_ConnectServer,
	results chan<- receiveResult,
) {
	for {
		frame, err := stream.Recv()
		select {
		case results <- receiveResult{frame: frame, err: err}:
		case <-ctx.Done():
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *Service) handleFrame(
	ctx context.Context,
	gatewayID string,
	routes *routeTable,
	outbound chan<- *orbitv1.ControlFrame,
	frame *orbitv1.GatewayFrame,
) error {
	if frame.GetHeartbeat() != nil {
		return nil
	}
	if online := frame.GetDeviceOnline(); online != nil {
		connectionID, err := session.NormalizeIdentifier("connection_id", online.ConnectionId)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		acquisition, err := session.NewAcquisition(online.DeviceId, gatewayID, online.ClientInstanceId)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		acquired, err := s.store.AcquireSession(ctx, acquisition)
		if err != nil {
			return status.Error(codes.Unavailable, "could not acquire device session")
		}
		return routes.setAndEnqueue(ctx, acquired.DeviceID, route{connectionID: connectionID, epoch: acquired.Epoch}, outbound, &orbitv1.ControlFrame{
			Body: &orbitv1.ControlFrame_DeviceSession{DeviceSession: &orbitv1.DeviceSession{
				ConnectionId: connectionID,
				DeviceId:     acquired.DeviceID,
				SessionEpoch: acquired.Epoch,
			}},
		})
	}
	if offline := frame.GetDeviceOffline(); offline != nil {
		if err := s.store.ReleaseSession(ctx, offline.DeviceId, gatewayID, offline.SessionEpoch); err != nil && !errors.Is(err, session.ErrStale) {
			return status.Error(codes.Unavailable, "could not release device session")
		}
		routes.remove(offline.DeviceId, offline.ConnectionId, offline.SessionEpoch)
		return nil
	}
	if started := frame.GetDeliveryStarted(); started != nil {
		_, err := s.store.MarkInFlight(
			ctx,
			started.CommandId,
			gatewayID,
			started.LeaseToken,
			started.SessionEpoch,
			"delivery-started:"+started.CommandId,
		)
		if err != nil {
			return mapStoreError(err)
		}
		return nil
	}
	if ackFrame := frame.GetCommandAck(); ackFrame != nil {
		ack := ackFrame.Ack
		if ack == nil {
			return status.Error(codes.InvalidArgument, "command_ack.ack is required")
		}
		var appliedAt *time.Time
		if ack.ClientAppliedAt != nil {
			if err := ack.ClientAppliedAt.CheckValid(); err != nil {
				return status.Error(codes.InvalidArgument, "client_applied_at is invalid")
			}
			value := ack.ClientAppliedAt.AsTime()
			appliedAt = &value
		}
		acknowledgement, err := command.NewAcknowledgement(
			ack.CommandId,
			ack.DeviceId,
			ack.SequenceNumber,
			gatewayID,
			ack.LeaseToken,
			ack.SessionEpoch,
			ack.ResultHash,
			appliedAt,
		)
		if err != nil {
			return status.Error(codes.InvalidArgument, err.Error())
		}
		if _, err := s.store.Acknowledge(ctx, acknowledgement, "ack:"+ack.CommandId); err != nil {
			return mapStoreError(err)
		}
		return nil
	}
	return status.Error(codes.InvalidArgument, "unsupported gateway frame")
}

func (s *Service) releaseRoutes(gatewayID string, routes *routeTable) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for deviceID, route := range routes.snapshot() {
		_ = s.store.ReleaseSession(ctx, deviceID, gatewayID, route.epoch)
	}
}

type route struct {
	connectionID string
	epoch        int64
}

type routeTable struct {
	mu     sync.RWMutex
	routes map[string]route
}

func newRouteTable() *routeTable { return &routeTable{routes: make(map[string]route)} }

func (t *routeTable) set(deviceID string, value route) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes[deviceID] = value
}

func (t *routeTable) setAndEnqueue(
	ctx context.Context,
	deviceID string,
	value route,
	outbound chan<- *orbitv1.ControlFrame,
	frame *orbitv1.ControlFrame,
) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.routes[deviceID] = value
	return enqueue(ctx, outbound, frame)
}

func (t *routeTable) get(deviceID string) (route, bool) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	value, ok := t.routes[deviceID]
	return value, ok
}

func (t *routeTable) remove(deviceID, connectionID string, epoch int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	current, ok := t.routes[deviceID]
	if ok && current.connectionID == connectionID && current.epoch == epoch {
		delete(t.routes, deviceID)
	}
}

func (t *routeTable) snapshot() map[string]route {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]route, len(t.routes))
	for key, value := range t.routes {
		result[key] = value
	}
	return result
}

type streamDispatcher struct {
	ctx      context.Context
	outbound chan<- *orbitv1.ControlFrame
	routes   *routeTable
}

func (d *streamDispatcher) Dispatch(ctx context.Context, lease command.Lease) error {
	if !lease.Command.ExpiresAt.After(time.Now().UTC()) {
		return fmt.Errorf("command %s expired before dispatch", lease.Command.ID)
	}
	route, ok := d.routes.get(lease.Command.DeviceID)
	if !ok || route.epoch != lease.SessionEpoch {
		return fmt.Errorf("no matching device route for epoch %d", lease.SessionEpoch)
	}
	frame := &orbitv1.ControlFrame{
		Body: &orbitv1.ControlFrame_CommandAssignment{CommandAssignment: &orbitv1.GatewayCommandAssignment{
			ConnectionId: route.connectionID,
			Command: &orbitv1.CommandDelivery{
				CommandId:      lease.Command.ID,
				DeviceId:       lease.Command.DeviceID,
				SequenceNumber: lease.Command.SequenceNumber,
				Priority:       int32(lease.Command.Priority),
				Payload:        append([]byte(nil), lease.Command.Payload...),
				PayloadHash:    append([]byte(nil), lease.Command.PayloadHash[:]...),
				ExpiresAt:      timestamppb.New(lease.Command.ExpiresAt),
				LeaseToken:     lease.Command.LeaseToken,
				SessionEpoch:   lease.SessionEpoch,
			},
		}},
	}
	return enqueue(ctx, d.outbound, frame)
}

func enqueue(ctx context.Context, outbound chan<- *orbitv1.ControlFrame, frame *orbitv1.ControlFrame) error {
	select {
	case outbound <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func mapStoreError(err error) error {
	switch {
	case errors.Is(err, command.ErrStaleLease), errors.Is(err, session.ErrStale):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		return status.Error(codes.Unavailable, "control-plane storage operation failed")
	}
}

func mapStreamError(err error) error {
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "gateway stream canceled")
	case errors.Is(err, heartbeat.ErrTimeout):
		return status.Error(codes.Unavailable, err.Error())
	default:
		return err
	}
}
