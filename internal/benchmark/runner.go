package benchmark

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	orbitv1 "github.com/JayYarlagadda/orbit/gen/orbit/v1"
	"github.com/JayYarlagadda/orbit/internal/client"
	"github.com/JayYarlagadda/orbit/internal/heartbeat"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type trackedCommand struct {
	id        string
	submitted time.Time
}

type applySink struct{}

func (applySink) Apply(_ context.Context, _ *orbitv1.CommandDelivery) ([]byte, error) {
	return []byte("bench-applied"), nil
}

type Runner struct {
	Config    Config
	StateRoot string
	runID     string
}

func (r *Runner) Run(ctx context.Context) (Summary, error) {
	if err := r.Config.Validate(); err != nil {
		return Summary{}, err
	}
	if r.StateRoot == "" {
		return Summary{}, errors.New("state root is required")
	}
	if err := os.MkdirAll(r.StateRoot, 0o755); err != nil {
		return Summary{}, fmt.Errorf("create state root: %w", err)
	}

	gitCommit, gitDirty, goVersion, host, err := CaptureEnvironment()
	if err != nil {
		return Summary{}, err
	}

	runID, err := randomID()
	if err != nil {
		return Summary{}, err
	}
	r.runID = runID

	startedAt := time.Now().UTC()
	deviceCtx, stopDevices := context.WithCancel(ctx)
	defer stopDevices()
	if err := r.startDevices(deviceCtx); err != nil {
		return Summary{}, err
	}
	r.waitForDevices()

	connection, err := grpc.NewClient(
		r.Config.ControlAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return Summary{}, fmt.Errorf("create control client: %w", err)
	}
	defer connection.Close()
	commandClient := orbitv1.NewCommandServiceClient(connection)

	if r.Config.WarmupCommands > 0 {
		if _, err := r.runTrial(ctx, commandClient, 0, true); err != nil {
			return Summary{}, fmt.Errorf("warmup trial: %w", err)
		}
	}

	trials := make([]TrialResult, 0, r.Config.Trials)
	for trial := 1; trial <= r.Config.Trials; trial++ {
		result, err := r.runTrial(ctx, commandClient, trial, false)
		if err != nil {
			return Summary{}, fmt.Errorf("trial %d: %w", trial, err)
		}
		trials = append(trials, result)
	}

	finishedAt := time.Now().UTC()
	summary := Summary{
		SchemaVersion: "1",
		BenchmarkName: r.Config.Name,
		MatrixID:      r.Config.MatrixID,
		GitCommit:     gitCommit,
		GitDirty:      gitDirty,
		GoVersion:     goVersion,
		Host:          host,
		StartedAt:     FormatRFC3339(startedAt),
		FinishedAt:    FormatRFC3339(finishedAt),
		Configuration: r.Config,
		Trials:        trials,
		Aggregate:     AggregateTrials(trials),
		BottleneckNotes: "First bottleneck is typically PostgreSQL commit latency on submit and " +
			"acknowledge paths under concurrent device load; gateway and client CPU remain secondary " +
			"until admission limits or scheduler batch size saturate.",
	}
	return summary, nil
}

func (r *Runner) startDevices(ctx context.Context) error {
	var workers sync.WaitGroup
	deviceErrors := make(chan error, r.Config.Clients)
	for index := 0; index < r.Config.Clients; index++ {
		deviceID := r.Config.DeviceID(index)
		statePath := filepath.Join(r.StateRoot, deviceID+".json")
		connection, err := grpc.NewClient(
			r.Config.GatewayAddress,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err != nil {
			return fmt.Errorf("create device client %s: %w", deviceID, err)
		}
		state, err := client.OpenStateStore(statePath, deviceID, 4096)
		if err != nil {
			_ = connection.Close()
			return fmt.Errorf("open state for %s: %w", deviceID, err)
		}
		instanceID, err := randomID()
		if err != nil {
			_ = connection.Close()
			return err
		}
		workers.Add(1)
		go func(deviceID, instanceID string, connection *grpc.ClientConn, state *client.StateStore) {
			defer workers.Done()
			defer connection.Close()
			err := client.RunSession(
				ctx,
				orbitv1.NewDeviceServiceClient(connection),
				client.SessionConfig{
					DeviceID:         deviceID,
					ClientInstanceID: instanceID,
					Heartbeat: heartbeat.Settings{
						Interval: 5 * time.Second,
						Timeout:  15 * time.Second,
					},
				},
				state,
				applySink{},
			)
			if err != nil && !errors.Is(err, context.Canceled) {
				select {
				case deviceErrors <- fmt.Errorf("device %s: %w", deviceID, err):
				default:
				}
			}
		}(deviceID, instanceID, connection, state)
	}
	return nil
}

func (r *Runner) runTrial(
	ctx context.Context,
	commandClient orbitv1.CommandServiceClient,
	trial int,
	warmup bool,
) (TrialResult, error) {
	commandCount := r.Config.Commands
	if warmup {
		commandCount = r.Config.WarmupCommands
	}
	payload := make([]byte, r.Config.PayloadBytes)
	if _, err := rand.Read(payload); err != nil {
		return TrialResult{}, err
	}

	trackedCommands := make([]trackedCommand, commandCount)
	var admissionRejected atomic.Int64

	sem := make(chan struct{}, r.Config.SubmitConcurrency)
	var submitGroup sync.WaitGroup
	submitErrors := make(chan error, 1)
	for index := 0; index < commandCount; index++ {
		submitGroup.Add(1)
		go func(index int) {
			defer submitGroup.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			deviceID := r.Config.DeviceID(index % r.Config.Clients)
			idempotencyKey := fmt.Sprintf("%s-%s-trial-%d-cmd-%d", r.Config.Name, r.runID, trial, index)
			submitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			submittedAt := time.Now().UTC()
			response, err := commandClient.SubmitCommand(submitCtx, &orbitv1.SubmitCommandRequest{
				ProducerId:     r.Config.ProducerID,
				IdempotencyKey: idempotencyKey,
				DeviceId:       deviceID,
				Priority:       int32(r.Config.Priority),
				Payload:        append([]byte(nil), payload...),
				ExpiresAt:      timestamppb.New(submittedAt.Add(r.Config.ExpiresAfter())),
			})
			if err != nil {
				if status.Code(err) == codes.ResourceExhausted {
					admissionRejected.Add(1)
					return
				}
				select {
				case submitErrors <- fmt.Errorf("submit command %d: %w", index, err):
				default:
				}
				return
			}
			trackedCommands[index] = trackedCommand{id: response.CommandId, submitted: submittedAt}
		}(index)
	}
	submitGroup.Wait()
	select {
	case err := <-submitErrors:
		return TrialResult{}, err
	default:
	}

	if warmup {
		if _, err := r.waitForAcks(ctx, commandClient, trackedCommands); err != nil {
			return TrialResult{}, err
		}
		return TrialResult{Trial: 0}, nil
	}

	measuredStart := time.Now()
	ackTimes, err := r.waitForAcks(ctx, commandClient, trackedCommands)
	if err != nil {
		return TrialResult{}, err
	}
	duration := time.Since(measuredStart)

	latencies := make([]float64, 0, len(ackTimes))
	acked := 0
	for _, item := range trackedCommands {
		if item.id == "" {
			continue
		}
		ackedAt, ok := ackTimes[item.id]
		if !ok {
			continue
		}
		acked++
		latencies = append(latencies, ackedAt.Sub(item.submitted).Seconds())
	}
	throughput := float64(acked) / duration.Seconds()
	return TrialResult{
		Trial:                  trial,
		CommandsSubmitted:      commandCount - int(admissionRejected.Load()),
		CommandsAcknowledged:   acked,
		AdmissionRejected:      int(admissionRejected.Load()),
		DurationSeconds:        duration.Seconds(),
		ThroughputAckPerSecond: throughput,
		LatencySeconds:         SummarizeLatencies(latencies),
	}, nil
}

func (r *Runner) waitForAcks(
	ctx context.Context,
	commandClient orbitv1.CommandServiceClient,
	commands []trackedCommand,
) (map[string]time.Time, error) {
	deadline := time.Now().Add(r.Config.TrialTimeout())
	ticker := time.NewTicker(r.Config.PollInterval())
	defer ticker.Stop()

	acknowledged := make(map[string]time.Time, len(commands))
	pending := make(map[string]struct{}, len(commands))
	for _, item := range commands {
		if item.id != "" {
			pending[item.id] = struct{}{}
		}
	}
	if len(pending) == 0 {
		return nil, errors.New("no commands were submitted")
	}

	pollOne := func(commandID string) bool {
		getCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		response, err := commandClient.GetCommand(getCtx, &orbitv1.GetCommandRequest{CommandId: commandID})
		if err != nil {
			return false
		}
		return response.State == orbitv1.CommandState_COMMAND_STATE_ACKNOWLEDGED
	}

	for len(pending) > 0 {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for %d commands to acknowledge", len(pending))
		}
		batch := make([]string, 0, len(pending))
		for commandID := range pending {
			batch = append(batch, commandID)
		}
		var mu sync.Mutex
		var found sync.WaitGroup
		workers := r.Config.SubmitConcurrency
		if workers < 1 {
			workers = 1
		}
		jobs := make(chan string, workers)
		for worker := 0; worker < workers; worker++ {
			found.Add(1)
			go func() {
				defer found.Done()
				for commandID := range jobs {
					if pollOne(commandID) {
						mu.Lock()
						if _, stillPending := pending[commandID]; stillPending {
							acknowledged[commandID] = time.Now().UTC()
							delete(pending, commandID)
						}
						mu.Unlock()
					}
				}
			}()
		}
		for _, commandID := range batch {
			jobs <- commandID
		}
		close(jobs)
		found.Wait()
		if len(pending) == 0 {
			return acknowledged, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
	return acknowledged, nil
}

func (r *Runner) waitForDevices() {
	wait := time.Duration(r.Config.Clients) * 200 * time.Millisecond
	if wait < 5*time.Second {
		wait = 5 * time.Second
	}
	if wait > 90*time.Second {
		wait = 90 * time.Second
	}
	time.Sleep(wait)
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
