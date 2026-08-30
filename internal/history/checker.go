package history

import (
	"fmt"
	"sort"
)

const (
	InvAcknowledgedTerminal = "INV-01"
	InvOneApplication       = "INV-02"
	InvDeviceOrder          = "INV-03"
	InvFencingMonotonicity  = "INV-05"
	InvSingleActiveSession  = "INV-08"
	InvTerminalUnblocks     = "INV-09"
)

type Violation struct {
	Invariant string `json:"invariant"`
	Message   string `json:"message"`
	Position  int    `json:"position"`
}

type Report struct {
	Passed     bool        `json:"passed"`
	Violations []Violation `json:"violations"`
}

func Check(record Record) Report {
	var violations []Violation
	violations = append(violations, checkAcknowledgedTerminal(record)...)
	violations = append(violations, checkOneApplication(record)...)
	violations = append(violations, checkDeviceOrder(record)...)
	violations = append(violations, checkFencingMonotonicity(record)...)
	violations = append(violations, checkSingleActiveSession(record)...)
	violations = append(violations, checkTerminalUnblocks(record)...)
	return Report{
		Passed:     len(violations) == 0,
		Violations: violations,
	}
}

func checkAcknowledgedTerminal(record Record) []Violation {
	acknowledged := make(map[string]struct{})
	for index, event := range record.AuditEvents {
		if event.NewState == "ACKNOWLEDGED" {
			acknowledged[event.CommandID] = struct{}{}
		}
		if event.OldState == "ACKNOWLEDGED" && event.NewState != "ACKNOWLEDGED" {
			return []Violation{{
				Invariant: InvAcknowledgedTerminal,
				Message:   fmt.Sprintf("command %s left ACKNOWLEDGED via %s", event.CommandID, event.NewState),
				Position:  index,
			}}
		}
	}
	for _, command := range record.Commands {
		if _, ok := acknowledged[command.ID]; ok && command.State != "ACKNOWLEDGED" {
			return []Violation{{
				Invariant: InvAcknowledgedTerminal,
				Message:   fmt.Sprintf("command %s is %s after being acknowledged", command.ID, command.State),
			}}
		}
	}
	return nil
}

func checkOneApplication(record Record) []Violation {
	counts := make(map[string]int)
	for index, application := range record.Applications {
		counts[application.CommandID]++
		if counts[application.CommandID] > 1 {
			return []Violation{{
				Invariant: InvOneApplication,
				Message:   fmt.Sprintf("command %s applied more than once", application.CommandID),
				Position:  index,
			}}
		}
	}
	return nil
}

func checkDeviceOrder(record Record) []Violation {
	byDevice := make(map[string][]ClientApplication)
	for _, application := range record.Applications {
		byDevice[application.DeviceID] = append(byDevice[application.DeviceID], application)
	}
	for deviceID, applications := range byDevice {
		sort.Slice(applications, func(i, j int) bool {
			return applications[i].SequenceNumber < applications[j].SequenceNumber
		})
		for index := 1; index < len(applications); index++ {
			if applications[index].SequenceNumber <= applications[index-1].SequenceNumber {
				return []Violation{{
					Invariant: InvDeviceOrder,
					Message: fmt.Sprintf(
						"device %s applied sequence %d after %d",
						deviceID,
						applications[index].SequenceNumber,
						applications[index-1].SequenceNumber,
					),
				}}
			}
		}
	}
	return nil
}

func checkFencingMonotonicity(record Record) []Violation {
	byCommand := make(map[string][]AuditEvent)
	for _, event := range record.AuditEvents {
		if event.LeaseToken > 0 {
			byCommand[event.CommandID] = append(byCommand[event.CommandID], event)
		}
	}
	for commandID, events := range byCommand {
		sort.Slice(events, func(i, j int) bool {
			return events[i].OccurredAt.Before(events[j].OccurredAt)
		})
		var previous int64
		for index, event := range events {
			if index > 0 && event.LeaseToken < previous {
				return []Violation{{
					Invariant: InvFencingMonotonicity,
					Message: fmt.Sprintf(
						"command %s lease token regressed from %d to %d",
						commandID,
						previous,
						event.LeaseToken,
					),
					Position: index,
				}}
			}
			previous = event.LeaseToken
		}
	}

	byDevice := make(map[string][]DeliveryAttempt)
	for _, attempt := range record.Attempts {
		byDevice[attempt.DeviceID] = append(byDevice[attempt.DeviceID], attempt)
	}
	for deviceID, attempts := range byDevice {
		sort.Slice(attempts, func(i, j int) bool {
			if attempts[i].StartedAt.Equal(attempts[j].StartedAt) {
				return attempts[i].SessionEpoch < attempts[j].SessionEpoch
			}
			return attempts[i].StartedAt.Before(attempts[j].StartedAt)
		})
		var previous int64
		for index, attempt := range attempts {
			if index > 0 && attempt.SessionEpoch < previous {
				return []Violation{{
					Invariant: InvFencingMonotonicity,
					Message: fmt.Sprintf(
						"device %s session epoch regressed from %d to %d",
						deviceID,
						previous,
						attempt.SessionEpoch,
					),
					Position: index,
				}}
			}
			previous = attempt.SessionEpoch
		}
	}
	return nil
}

func checkSingleActiveSession(record Record) []Violation {
	byDevice := make(map[string][]DeliveryAttempt)
	for _, attempt := range record.Attempts {
		if attempt.Outcome != "ACKNOWLEDGED" {
			continue
		}
		byDevice[attempt.DeviceID] = append(byDevice[attempt.DeviceID], attempt)
	}
	for deviceID, attempts := range byDevice {
		sort.Slice(attempts, func(i, j int) bool {
			return attempts[i].StartedAt.Before(attempts[j].StartedAt)
		})
		var previous int64
		for index, attempt := range attempts {
			if index > 0 && attempt.SessionEpoch < previous {
				return []Violation{{
					Invariant: InvSingleActiveSession,
					Message: fmt.Sprintf(
						"device %s acknowledged under stale session epoch %d after epoch %d",
						deviceID,
						attempt.SessionEpoch,
						previous,
					),
					Position: index,
				}}
			}
			previous = attempt.SessionEpoch
		}
	}
	return nil
}

func checkTerminalUnblocks(record Record) []Violation {
	terminalStates := map[string]struct{}{
		"ACKNOWLEDGED": {},
		"EXPIRED":      {},
		"DEAD_LETTER":  {},
		"CANCELLED":    {},
	}
	byDevice := make(map[string][]CommandSnapshot)
	for _, command := range record.Commands {
		byDevice[command.DeviceID] = append(byDevice[command.DeviceID], command)
	}
	for deviceID, commands := range byDevice {
		sort.Slice(commands, func(i, j int) bool {
			return commands[i].SequenceNumber < commands[j].SequenceNumber
		})
		var highestTerminal int64
		for _, command := range commands {
			if _, ok := terminalStates[command.State]; ok {
				if command.SequenceNumber > highestTerminal {
					highestTerminal = command.SequenceNumber
				}
				continue
			}
			if highestTerminal > 0 && command.SequenceNumber > highestTerminal+1 {
				return []Violation{{
					Invariant: InvTerminalUnblocks,
					Message: fmt.Sprintf(
						"device %s has non-terminal sequence %d after terminal sequence %d",
						deviceID,
						command.SequenceNumber,
						highestTerminal,
					),
				}}
			}
		}
	}
	return nil
}
