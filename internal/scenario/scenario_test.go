package scenario

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadExample(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "scenarios", "examples", "offline-reconnect.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	s, err := Load(file)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if s.Name != "offline-reconnect" || len(s.Events) != 4 {
		t.Fatalf("unexpected scenario: name=%q events=%d", s.Name, len(s.Events))
	}
}

func TestLoadRejectsInvalidFixture(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "scenarios", "invalid", "unknown-device.v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()

	_, err = Load(file)
	if err == nil || !strings.Contains(err.Error(), "unknown device") {
		t.Fatalf("Load() error = %v, want unknown device", err)
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	document := `{
		"schema_version":"1",
		"name":"unknown-field",
		"seed":"1",
		"duration_ms":1,
		"topology":{"gateways":["gateway-a"],"devices":["device-a"]},
		"network_profile":{"latency_ms":0,"jitter_ms":0,"delivery_loss_rate":0,"ack_loss_rate":0,"duplicate_rate":0},
		"events":[],
		"unexpected":true
	}`

	_, err := Load(strings.NewReader(document))
	if err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestValidateRejectsNonCanonicalSeed(t *testing.T) {
	for _, seed := range []string{"", "+1", "01", "18446744073709551616"} {
		t.Run(seed, func(t *testing.T) {
			s := validScenario()
			s.Seed = seed
			if err := s.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestValidateRejectsOutOfOrderEvents(t *testing.T) {
	s := validScenario()
	s.Events = []Event{
		{AtMS: 2, Type: EventDeviceDisconnect, DeviceID: "device-a"},
		{AtMS: 1, Type: EventDeviceReconnect, DeviceID: "device-a"},
	}
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "ordered") {
		t.Fatalf("Validate() error = %v, want ordering error", err)
	}
}

func TestValidatePreservesEqualTimestampOrder(t *testing.T) {
	s := validScenario()
	s.Events = []Event{
		{AtMS: 2, Type: EventDeviceDisconnect, DeviceID: "device-a"},
		{AtMS: 2, Type: EventDeviceReconnect, DeviceID: "device-a"},
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validScenario() Scenario {
	return Scenario{
		SchemaVersion: SchemaVersionV1,
		Name:          "valid",
		Seed:          "1",
		DurationMS:    10,
		Topology: Topology{
			Gateways: []string{"gateway-a"},
			Devices:  []string{"device-a"},
		},
	}
}
