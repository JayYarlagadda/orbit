package faultschedule

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/JayYarlagadda/orbit/internal/scenario"
)

// Controller applies compiled logical transport faults at deterministic offsets
// from a shared scenario start time.
type Controller struct {
	startedAt time.Time
	events    []scenario.ScheduleEvent
	nextIndex int

	mu      sync.Mutex
	pending map[string]pendingFault
}

type pendingFault struct {
	dropDelivery      int
	duplicateDelivery int
	dropAck           int
	duplicateAck      int
}

func LoadController(path string, startedAt time.Time) (*Controller, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read fault schedule: %w", err)
	}
	var schedule scenario.Schedule
	if err := json.Unmarshal(content, &schedule); err != nil {
		return nil, fmt.Errorf("decode fault schedule: %w", err)
	}
	return NewController(schedule, startedAt), nil
}

func NewController(schedule scenario.Schedule, startedAt time.Time) *Controller {
	events := append([]scenario.ScheduleEvent(nil), schedule.Events...)
	return &Controller{
		startedAt: startedAt.UTC(),
		events:    events,
		pending:   make(map[string]pendingFault),
	}
}

func (c *Controller) elapsedMS() uint64 {
	if c == nil {
		return 0
	}
	elapsed := time.Since(c.startedAt)
	if elapsed < 0 {
		return 0
	}
	return uint64(elapsed.Milliseconds())
}

func (c *Controller) advance() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	elapsed := c.elapsedMS()
	for c.nextIndex < len(c.events) && c.events[c.nextIndex].AtMS <= elapsed {
		event := c.events[c.nextIndex]
		c.nextIndex++
		deviceID := event.DeviceID
		if deviceID == "" {
			continue
		}
		state := c.pending[deviceID]
		switch event.Type {
		case "delivery_drop":
			state.dropDelivery++
		case "delivery_duplicate":
			state.duplicateDelivery++
		case "ack_drop":
			state.dropAck++
		case "ack_duplicate":
			state.duplicateAck++
		}
		c.pending[deviceID] = state
	}
}

func (c *Controller) ConsumeDeliveryDrop(deviceID string) bool {
	return c.consumeDeliveryDrop(deviceID)
}

func (c *Controller) ConsumeDuplicateDelivery(deviceID string) bool {
	return c.consumeDuplicateDelivery(deviceID)
}

func (c *Controller) ConsumeAckDrop(deviceID string) bool {
	return c.consumeAckDrop(deviceID)
}

func (c *Controller) ConsumeDuplicateAck(deviceID string) bool {
	return c.consumeDuplicateAck(deviceID)
}

func (c *Controller) consumeDeliveryDrop(deviceID string) bool {
	c.advance()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.pending[deviceID]
	if state.dropDelivery == 0 {
		return false
	}
	state.dropDelivery--
	c.pending[deviceID] = state
	return true
}

func (c *Controller) consumeDuplicateDelivery(deviceID string) bool {
	c.advance()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.pending[deviceID]
	if state.duplicateDelivery == 0 {
		return false
	}
	state.duplicateDelivery--
	c.pending[deviceID] = state
	return true
}

func (c *Controller) consumeAckDrop(deviceID string) bool {
	c.advance()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.pending[deviceID]
	if state.dropAck == 0 {
		return false
	}
	state.dropAck--
	c.pending[deviceID] = state
	return true
}

func (c *Controller) consumeDuplicateAck(deviceID string) bool {
	c.advance()
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.pending[deviceID]
	if state.duplicateAck == 0 {
		return false
	}
	state.duplicateAck--
	c.pending[deviceID] = state
	return true
}
