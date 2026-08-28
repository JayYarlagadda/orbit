package gateway

import (
	"context"
	"errors"
	"fmt"
	"sync"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/session"
)

var ErrControlDisconnected = errors.New("gateway control stream disconnected")

type HubConfig struct {
	GatewayID        string
	ControlBuffer    int
	ConnectionBuffer int
}

type Hub struct {
	gatewayID        string
	connectionBuffer int
	outbound         chan *orbitv1.GatewayFrame
	done             chan struct{}
	failOnce         sync.Once
	mu               sync.RWMutex
	connections      map[string]*Connection
}

type Connection struct {
	ID           string
	DeviceID     string
	SessionEpoch int64
	frames       chan *orbitv1.ControlFrame
}

func NewHub(config HubConfig) (*Hub, error) {
	gatewayID, err := session.NormalizeIdentifier("gateway_id", config.GatewayID)
	if err != nil {
		return nil, err
	}
	if config.ControlBuffer < 1 || config.ControlBuffer > 4096 {
		return nil, errors.New("control buffer must be between 1 and 4096")
	}
	if config.ConnectionBuffer < 1 || config.ConnectionBuffer > 256 {
		return nil, errors.New("connection buffer must be between 1 and 256")
	}
	return &Hub{
		gatewayID:        gatewayID,
		connectionBuffer: config.ConnectionBuffer,
		outbound:         make(chan *orbitv1.GatewayFrame, config.ControlBuffer),
		done:             make(chan struct{}),
		connections:      make(map[string]*Connection),
	}, nil
}

func (h *Hub) Outbound() <-chan *orbitv1.GatewayFrame { return h.outbound }
func (h *Hub) Done() <-chan struct{}                  { return h.done }

func (h *Hub) Fail() {
	h.failOnce.Do(func() { close(h.done) })
}

func (h *Hub) Register(
	ctx context.Context,
	connectionID string,
	deviceID string,
	clientInstanceID string,
) (*Connection, error) {
	if _, err := session.NormalizeIdentifier("connection_id", connectionID); err != nil {
		return nil, err
	}
	acquisition, err := session.NewAcquisition(deviceID, h.gatewayID, clientInstanceID)
	if err != nil {
		return nil, err
	}
	connection := &Connection{
		ID:       connectionID,
		DeviceID: acquisition.DeviceID,
		frames:   make(chan *orbitv1.ControlFrame, h.connectionBuffer),
	}
	h.mu.Lock()
	if _, exists := h.connections[connectionID]; exists {
		h.mu.Unlock()
		return nil, fmt.Errorf("connection ID %q is already registered", connectionID)
	}
	h.connections[connectionID] = connection
	h.mu.Unlock()
	registered := false
	defer func() {
		if !registered {
			h.remove(connectionID, connection)
		}
	}()

	if err := h.send(ctx, &orbitv1.GatewayFrame{
		Body: &orbitv1.GatewayFrame_DeviceOnline{DeviceOnline: &orbitv1.DeviceOnline{
			ConnectionId:     connectionID,
			DeviceId:         acquisition.DeviceID,
			ClientInstanceId: acquisition.ClientInstanceID,
		}},
	}); err != nil {
		return nil, err
	}

	var pending []*orbitv1.ControlFrame
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-h.done:
			return nil, ErrControlDisconnected
		case frame := <-connection.frames:
			opened := frame.GetDeviceSession()
			if opened == nil {
				// Frames seen before the session opens are replayed below.
				// Bound the backlog so a control plane that never sends a
				// session frame cannot grow it without limit.
				if len(pending) >= h.connectionBuffer {
					return nil, fmt.Errorf(
						"control plane sent more than %d frames before opening the device session",
						h.connectionBuffer,
					)
				}
				pending = append(pending, frame)
				continue
			}
			if opened.DeviceId != connection.DeviceID || opened.ConnectionId != connection.ID || opened.SessionEpoch < 1 {
				return nil, errors.New("control plane returned an invalid device session")
			}
			connection.SessionEpoch = opened.SessionEpoch
			// Register is the only reader of this channel until it returns, so
			// a blocking replay here would deadlock against a full buffer.
			for _, queued := range pending {
				select {
				case connection.frames <- queued:
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-h.done:
					return nil, ErrControlDisconnected
				}
			}
			registered = true
			return connection, nil
		}
	}
}

func (h *Hub) Unregister(ctx context.Context, connection *Connection) error {
	if connection == nil {
		return nil
	}
	h.remove(connection.ID, connection)
	return h.send(ctx, &orbitv1.GatewayFrame{
		Body: &orbitv1.GatewayFrame_DeviceOffline{DeviceOffline: &orbitv1.DeviceOffline{
			ConnectionId: connection.ID,
			DeviceId:     connection.DeviceID,
			SessionEpoch: connection.SessionEpoch,
		}},
	})
}

func (h *Hub) Deliver(ctx context.Context, frame *orbitv1.ControlFrame) error {
	connectionID := ""
	if opened := frame.GetDeviceSession(); opened != nil {
		connectionID = opened.ConnectionId
	} else if assignment := frame.GetCommandAssignment(); assignment != nil {
		connectionID = assignment.ConnectionId
	} else {
		return errors.New("unsupported control frame")
	}
	h.mu.RLock()
	connection := h.connections[connectionID]
	h.mu.RUnlock()
	if connection == nil {
		return nil
	}
	select {
	case connection.frames <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return ErrControlDisconnected
	}
}

func (h *Hub) ReportDeliveryStarted(ctx context.Context, delivery *orbitv1.CommandDelivery) error {
	return h.send(ctx, &orbitv1.GatewayFrame{
		Body: &orbitv1.GatewayFrame_DeliveryStarted{DeliveryStarted: &orbitv1.DeliveryStarted{
			CommandId:    delivery.CommandId,
			DeviceId:     delivery.DeviceId,
			LeaseToken:   delivery.LeaseToken,
			SessionEpoch: delivery.SessionEpoch,
		}},
	})
}

func (h *Hub) ReportAcknowledgement(ctx context.Context, acknowledgement *orbitv1.CommandAck) error {
	return h.send(ctx, &orbitv1.GatewayFrame{
		Body: &orbitv1.GatewayFrame_CommandAck{CommandAck: &orbitv1.GatewayCommandAck{Ack: acknowledgement}},
	})
}

func (h *Hub) send(ctx context.Context, frame *orbitv1.GatewayFrame) error {
	select {
	case h.outbound <- frame:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-h.done:
		return ErrControlDisconnected
	}
}

func (h *Hub) remove(connectionID string, expected *Connection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.connections[connectionID] == expected {
		delete(h.connections, connectionID)
	}
}

func (c *Connection) Frames() <-chan *orbitv1.ControlFrame { return c.frames }
