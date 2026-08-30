// Package scenario defines the versioned contract shared by the fault engine
// and the real-system scenario runner.
package scenario

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

const (
	SchemaVersionV1 = "1"
	maxDurationMS   = 24 * 60 * 60 * 1000
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

type EventType string

const (
	EventDeviceDisconnect    EventType = "device_disconnect"
	EventDeviceReconnect     EventType = "device_reconnect"
	EventDeviceGatewaySwitch EventType = "device_gateway_switch"
	EventGatewayCrash        EventType = "gateway_crash"
	EventGatewayRecover      EventType = "gateway_recover"
	EventTransportProfile    EventType = "transport_profile"
)

// Scenario is the canonical in-memory representation of a version 1 scenario.
// Events with the same timestamp retain their document order as a stable
// tie-breaker across language implementations.
type Scenario struct {
	SchemaURI     string         `json:"$schema,omitempty"`
	SchemaVersion string         `json:"schema_version"`
	Name          string         `json:"name"`
	Seed          string         `json:"seed"`
	DurationMS    uint64         `json:"duration_ms"`
	Topology      Topology       `json:"topology"`
	Network       NetworkProfile `json:"network_profile"`
	Events        []Event        `json:"events"`
}

type Topology struct {
	Gateways []string `json:"gateways"`
	Devices  []string `json:"devices"`
}

type NetworkProfile struct {
	LatencyMS        uint64  `json:"latency_ms"`
	JitterMS         uint64  `json:"jitter_ms"`
	DeliveryLossRate float64 `json:"delivery_loss_rate"`
	AckLossRate      float64 `json:"ack_loss_rate"`
	DuplicateRate    float64 `json:"duplicate_rate"`
}

type Event struct {
	AtMS      uint64          `json:"at_ms"`
	Type      EventType       `json:"type"`
	DeviceID  string          `json:"device_id,omitempty"`
	GatewayID string          `json:"gateway_id,omitempty"`
	Profile   *NetworkProfile `json:"profile,omitempty"`
}

// Load decodes exactly one scenario document, rejects unknown fields, and
// validates all cross-field constraints that JSON Schema cannot express.
func Load(r io.Reader) (Scenario, error) {
	var s Scenario
	decoder := json.NewDecoder(r)
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&s); err != nil {
		return Scenario{}, fmt.Errorf("decode scenario: %w", err)
	}

	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Scenario{}, errors.New("decode scenario: multiple JSON values")
		}
		return Scenario{}, fmt.Errorf("decode scenario trailing data: %w", err)
	}

	if err := s.Validate(); err != nil {
		return Scenario{}, err
	}
	return s, nil
}

func (s Scenario) Validate() error {
	if s.SchemaURI != "" && s.SchemaURI != "../schema/v1/scenario.schema.json" {
		return fmt.Errorf("$schema: unsupported schema reference %q", s.SchemaURI)
	}
	if s.SchemaVersion != SchemaVersionV1 {
		return fmt.Errorf("schema_version: expected %q, got %q", SchemaVersionV1, s.SchemaVersion)
	}
	if strings.TrimSpace(s.Name) == "" || len(s.Name) > 100 {
		return errors.New("name: must contain 1 to 100 non-whitespace characters")
	}
	if err := validateSeed(s.Seed); err != nil {
		return err
	}
	if s.DurationMS == 0 || s.DurationMS > maxDurationMS {
		return fmt.Errorf("duration_ms: must be between 1 and %d", maxDurationMS)
	}

	gateways, err := validateIdentifiers("topology.gateways", s.Topology.Gateways)
	if err != nil {
		return err
	}
	devices, err := validateIdentifiers("topology.devices", s.Topology.Devices)
	if err != nil {
		return err
	}
	if err := s.Network.validate("network_profile"); err != nil {
		return err
	}

	var previous uint64
	for index, event := range s.Events {
		path := fmt.Sprintf("events[%d]", index)
		if event.AtMS > s.DurationMS {
			return fmt.Errorf("%s.at_ms: exceeds duration_ms", path)
		}
		if index > 0 && event.AtMS < previous {
			return fmt.Errorf("%s.at_ms: events must be ordered by timestamp", path)
		}
		previous = event.AtMS
		if err := event.validate(path, gateways, devices); err != nil {
			return err
		}
	}
	return nil
}

func validateSeed(seed string) error {
	if seed == "" || strings.HasPrefix(seed, "+") || (len(seed) > 1 && seed[0] == '0') {
		return errors.New("seed: must be a canonical unsigned 64-bit decimal string")
	}
	if _, err := strconv.ParseUint(seed, 10, 64); err != nil {
		return fmt.Errorf("seed: must be a canonical unsigned 64-bit decimal string: %w", err)
	}
	return nil
}

func validateIdentifiers(path string, values []string) (map[string]struct{}, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s: must contain at least one identifier", path)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if !identifierPattern.MatchString(value) {
			return nil, fmt.Errorf("%s[%d]: invalid identifier %q", path, index, value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s[%d]: duplicate identifier %q", path, index, value)
		}
		seen[value] = struct{}{}
	}
	return seen, nil
}

func (p NetworkProfile) validate(path string) error {
	if p.LatencyMS > 60*60*1000 {
		return fmt.Errorf("%s.latency_ms: must not exceed one hour", path)
	}
	if p.JitterMS > 60*60*1000 {
		return fmt.Errorf("%s.jitter_ms: must not exceed one hour", path)
	}
	for name, value := range map[string]float64{
		"delivery_loss_rate": p.DeliveryLossRate,
		"ack_loss_rate":      p.AckLossRate,
		"duplicate_rate":     p.DuplicateRate,
	} {
		if value < 0 || value > 1 {
			return fmt.Errorf("%s.%s: must be between 0 and 1", path, name)
		}
	}
	return nil
}

func (e Event) validate(path string, gateways, devices map[string]struct{}) error {
	switch e.Type {
	case EventDeviceDisconnect, EventDeviceReconnect:
		if e.GatewayID != "" || e.Profile != nil {
			return fmt.Errorf("%s: %s permits only device_id", path, e.Type)
		}
		if _, exists := devices[e.DeviceID]; !exists {
			return fmt.Errorf("%s.device_id: unknown device %q", path, e.DeviceID)
		}
	case EventDeviceGatewaySwitch:
		if e.Profile != nil {
			return fmt.Errorf("%s: device_gateway_switch permits only device_id and gateway_id", path)
		}
		if _, exists := devices[e.DeviceID]; !exists {
			return fmt.Errorf("%s.device_id: unknown device %q", path, e.DeviceID)
		}
		if _, exists := gateways[e.GatewayID]; !exists {
			return fmt.Errorf("%s.gateway_id: unknown gateway %q", path, e.GatewayID)
		}
	case EventGatewayCrash, EventGatewayRecover:
		if e.DeviceID != "" || e.Profile != nil {
			return fmt.Errorf("%s: %s permits only gateway_id", path, e.Type)
		}
		if _, exists := gateways[e.GatewayID]; !exists {
			return fmt.Errorf("%s.gateway_id: unknown gateway %q", path, e.GatewayID)
		}
	case EventTransportProfile:
		if e.GatewayID != "" || e.Profile == nil {
			return fmt.Errorf("%s: transport_profile requires device_id and profile", path)
		}
		if e.DeviceID != "*" {
			if _, exists := devices[e.DeviceID]; !exists {
				return fmt.Errorf("%s.device_id: unknown device %q", path, e.DeviceID)
			}
		}
		if err := e.Profile.validate(path + ".profile"); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%s.type: unsupported event type %q", path, e.Type)
	}
	return nil
}
