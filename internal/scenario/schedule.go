package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

const (
	PrngAlgorithm    = "splitmix64-v1"
	transportTickMS  = uint64(1000)
	scheduleSchemaV1 = "1"
)

type scheduleQueuedEvent struct {
	atMS      uint64
	ordinal   uint64
	eventType string
	deviceID  string
	gatewayID string
	profile   *NetworkProfile
}

type Schedule struct {
	SchemaVersion string          `json:"schema_version"`
	ScenarioName  string          `json:"scenario_name"`
	ScenarioSeed  string          `json:"scenario_seed"`
	PrngAlgorithm string          `json:"prng_algorithm"`
	DurationMS    uint64          `json:"duration_ms"`
	Events        []ScheduleEvent `json:"events"`
}

type ScheduleEvent struct {
	AtMS      uint64          `json:"at_ms"`
	Ordinal   uint64          `json:"ordinal"`
	Type      string          `json:"type"`
	DeviceID  string          `json:"device_id,omitempty"`
	GatewayID string          `json:"gateway_id,omitempty"`
	Profile   *NetworkProfile `json:"profile,omitempty"`
}

type splitMix64 struct {
	state uint64
}

func newSplitMix64(seed uint64) *splitMix64 {
	return &splitMix64{state: seed}
}

func (g *splitMix64) nextUint64() uint64 {
	g.state += 0x9e3779b97f4a7c15
	value := g.state
	value = (value ^ (value >> 30)) * 0xbf58476d1ce4e5b9
	value = (value ^ (value >> 27)) * 0x94d049bb133111eb
	return value ^ (value >> 31)
}

func (g *splitMix64) nextUnitDouble() float64 {
	return float64(g.nextUint64()>>11) * (1.0 / float64(uint64(1)<<53))
}

func CompileSchedule(document Scenario) (Schedule, error) {
	seed, err := strconv.ParseUint(document.Seed, 10, 64)
	if err != nil {
		return Schedule{}, fmt.Errorf("parse seed: %w", err)
	}

	queue := make([]scheduleQueuedEvent, 0, len(document.Events)+32)
	var ordinal uint64
	for _, event := range document.Events {
		item := scheduleQueuedEvent{
			atMS:      event.AtMS,
			ordinal:   ordinal,
			eventType: string(event.Type),
		}
		switch event.Type {
		case EventTransportProfile:
			item.deviceID = event.DeviceID
			item.profile = event.Profile
		case EventGatewayCrash, EventGatewayRecover:
			item.gatewayID = event.GatewayID
		default:
			item.deviceID = event.DeviceID
		}
		queue = append(queue, item)
		ordinal++
	}

	prng := newSplitMix64(seed)
	appendTransportFaults(&queue, &ordinal, prng, document, document.Network)

	sort.SliceStable(queue, func(i, j int) bool {
		if queue[i].atMS != queue[j].atMS {
			return queue[i].atMS < queue[j].atMS
		}
		return queue[i].ordinal < queue[j].ordinal
	})

	events := make([]ScheduleEvent, len(queue))
	for index, item := range queue {
		events[index] = ScheduleEvent{
			AtMS:      item.atMS,
			Ordinal:   item.ordinal,
			Type:      item.eventType,
			DeviceID:  item.deviceID,
			GatewayID: item.gatewayID,
			Profile:   item.profile,
		}
	}

	return Schedule{
		SchemaVersion: scheduleSchemaV1,
		ScenarioName:  document.Name,
		ScenarioSeed:  document.Seed,
		PrngAlgorithm: PrngAlgorithm,
		DurationMS:    document.DurationMS,
		Events:        events,
	}, nil
}

func appendTransportFaults(queue *[]scheduleQueuedEvent, ordinal *uint64, prng *splitMix64, document Scenario, profile NetworkProfile) {
	for tick := uint64(0); tick < document.DurationMS; tick += transportTickMS {
		for _, device := range document.Topology.Devices {
			deliveryRoll := prng.nextUnitDouble()
			latency := profile.LatencyMS + jitterMS(prng, profile.JitterMS)
			if deliveryRoll < profile.DeliveryLossRate {
				at := tick + latency
				if at <= document.DurationMS {
					*queue = append(*queue, scheduleQueuedEvent{
						atMS:      at,
						ordinal:   *ordinal,
						eventType: "delivery_drop",
						deviceID:  device,
					})
					*ordinal++
				}
			} else if deliveryRoll < profile.DeliveryLossRate+profile.DuplicateRate {
				at := tick + latency
				if at <= document.DurationMS {
					*queue = append(*queue, scheduleQueuedEvent{
						atMS:      at,
						ordinal:   *ordinal,
						eventType: "delivery_duplicate",
						deviceID:  device,
					})
					*ordinal++
				}
			}

			ackRoll := prng.nextUnitDouble()
			ackLatency := profile.LatencyMS*2 + jitterMS(prng, profile.JitterMS)
			if ackRoll < profile.AckLossRate {
				at := tick + ackLatency
				if at <= document.DurationMS {
					*queue = append(*queue, scheduleQueuedEvent{
						atMS:      at,
						ordinal:   *ordinal,
						eventType: "ack_drop",
						deviceID:  device,
					})
					*ordinal++
				}
			} else if ackRoll < profile.AckLossRate+profile.DuplicateRate {
				at := tick + ackLatency
				if at <= document.DurationMS {
					*queue = append(*queue, scheduleQueuedEvent{
						atMS:      at,
						ordinal:   *ordinal,
						eventType: "ack_duplicate",
						deviceID:  device,
					})
					*ordinal++
				}
			}
		}
	}
}

func jitterMS(prng *splitMix64, maximum uint64) uint64 {
	if maximum == 0 {
		return 0
	}
	return prng.nextUint64() % (maximum + 1)
}

func CanonicalScheduleJSON(schedule Schedule) (string, error) {
	var buffer bytes.Buffer
	buffer.WriteString("{\n")
	writeJSONStringField(&buffer, "schema_version", schedule.SchemaVersion, 2)
	writeJSONStringField(&buffer, "scenario_name", schedule.ScenarioName, 2)
	writeJSONStringField(&buffer, "scenario_seed", schedule.ScenarioSeed, 2)
	writeJSONStringField(&buffer, "prng_algorithm", schedule.PrngAlgorithm, 2)
	fmt.Fprintf(&buffer, "  \"duration_ms\": %d,\n", schedule.DurationMS)
	buffer.WriteString("  \"events\": [\n")
	for index, event := range schedule.Events {
		buffer.WriteString("    {\n")
		fmt.Fprintf(&buffer, "      \"at_ms\": %d,\n", event.AtMS)
		fmt.Fprintf(&buffer, "      \"ordinal\": %d,\n", event.Ordinal)
		buffer.WriteString("      \"type\": ")
		buffer.WriteString(jsonString(event.Type))
		if event.DeviceID != "" || event.GatewayID != "" || event.Profile != nil {
			buffer.WriteString(",\n")
		} else {
			buffer.WriteByte('\n')
		}
		if event.DeviceID != "" {
			buffer.WriteString("      \"device_id\": ")
			buffer.WriteString(jsonString(event.DeviceID))
			if event.Profile != nil {
				buffer.WriteString(",\n")
			} else {
				buffer.WriteByte('\n')
			}
		}
		if event.GatewayID != "" {
			buffer.WriteString("      \"gateway_id\": ")
			buffer.WriteString(jsonString(event.GatewayID))
			buffer.WriteByte('\n')
		}
		if event.Profile != nil {
			buffer.WriteString("      \"profile\": {\n")
			fmt.Fprintf(&buffer, "        \"latency_ms\": %d,\n", event.Profile.LatencyMS)
			fmt.Fprintf(&buffer, "        \"jitter_ms\": %d,\n", event.Profile.JitterMS)
			fmt.Fprintf(&buffer, "        \"delivery_loss_rate\": %s,\n", formatFloat(event.Profile.DeliveryLossRate))
			fmt.Fprintf(&buffer, "        \"ack_loss_rate\": %s,\n", formatFloat(event.Profile.AckLossRate))
			fmt.Fprintf(&buffer, "        \"duplicate_rate\": %s\n", formatFloat(event.Profile.DuplicateRate))
			buffer.WriteString("      }\n")
		}
		buffer.WriteString("    }")
		if index+1 < len(schedule.Events) {
			buffer.WriteString(",")
		}
		buffer.WriteString("\n")
	}
	buffer.WriteString("  ]\n")
	buffer.WriteString("}\n")
	return buffer.String(), nil
}

func writeJSONStringField(buffer *bytes.Buffer, key, value string, indent int) {
	fmt.Fprintf(buffer, "%s\"%s\": %s,\n", strings.Repeat(" ", indent), key, jsonString(value))
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'g', 17, 64)
}

// CompileScheduleCanonical returns the canonical JSON schedule for a scenario
// document. The output must remain byte-identical to the C++ fault engine.
func CompileScheduleCanonical(document Scenario) (string, error) {
	schedule, err := CompileSchedule(document)
	if err != nil {
		return "", err
	}
	return CanonicalScheduleJSON(schedule)
}
