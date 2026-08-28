package gatewaycontrol

import (
	"context"
	"testing"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/command"
)

func TestRouteTableRemovalIsFenced(t *testing.T) {
	routes := newRouteTable()
	routes.set("device-1", route{connectionID: "connection-2", epoch: 2})
	routes.remove("device-1", "connection-1", 1)
	if got, ok := routes.get("device-1"); !ok || got.epoch != 2 {
		t.Fatalf("stale removal changed route: (%+v, %v)", got, ok)
	}
	routes.remove("device-1", "connection-2", 2)
	if _, ok := routes.get("device-1"); ok {
		t.Fatal("matching removal left route active")
	}
}

func TestStreamDispatcherRoutesMatchingEpoch(t *testing.T) {
	routes := newRouteTable()
	routes.set("device-1", route{connectionID: "connection-1", epoch: 4})
	outbound := make(chan *orbitv1.ControlFrame, 1)
	dispatcher := &streamDispatcher{ctx: context.Background(), outbound: outbound, routes: routes}
	lease := command.Lease{
		Command: command.Command{
			ID:             "76386381-325e-49c6-82d1-afd7f140fcaf",
			DeviceID:       "device-1",
			SequenceNumber: 9,
			ExpiresAt:      time.Now().Add(time.Hour),
			LeaseToken:     3,
		},
		SessionEpoch: 4,
	}
	if err := dispatcher.Dispatch(context.Background(), lease); err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	frame := <-outbound
	if frame.GetCommandAssignment().ConnectionId != "connection-1" || frame.GetCommandAssignment().Command.LeaseToken != 3 {
		t.Fatalf("Dispatch() frame = %+v", frame)
	}
	lease.SessionEpoch = 5
	if err := dispatcher.Dispatch(context.Background(), lease); err == nil {
		t.Fatal("Dispatch() with stale route unexpectedly succeeded")
	}
}
