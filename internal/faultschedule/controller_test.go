package faultschedule

import (
	"testing"
	"time"

	"github.com/JayYarlagadda/orbit/internal/scenario"
)

func TestControllerAppliesDeliveryDropOnce(t *testing.T) {
	startedAt := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	controller := NewController(scenario.Schedule{
		Events: []scenario.ScheduleEvent{
			{AtMS: 0, Type: "delivery_drop", DeviceID: "device-a"},
		},
	}, startedAt)

	if !controller.ConsumeDeliveryDrop("device-a") {
		t.Fatal("expected first delivery to be dropped")
	}
	if controller.ConsumeDeliveryDrop("device-a") {
		t.Fatal("expected drop to be one-shot")
	}
}

func TestControllerWaitsUntilElapsedTime(t *testing.T) {
	startedAt := time.Now().Add(-10 * time.Millisecond)
	controller := NewController(scenario.Schedule{
		Events: []scenario.ScheduleEvent{
			{AtMS: 50, Type: "delivery_drop", DeviceID: "device-a"},
		},
	}, startedAt)
	if controller.ConsumeDeliveryDrop("device-a") {
		t.Fatal("fault should not be active before at_ms")
	}
}
