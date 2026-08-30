package scenariorunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/JayYarlagadda/orbit/internal/history"
	"github.com/JayYarlagadda/orbit/internal/scenario"
	"github.com/JayYarlagadda/orbit/internal/storage/migrate"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Binaries struct {
	Orbitd   string
	Gateway  string
	Client   string
	Orbitctl string
	Migrate  string
}

type Config struct {
	DatabaseURL  string
	ScenarioPath string
	WorkDir      string
	Binaries     Binaries
	Timeout      time.Duration
}

type Result struct {
	Record history.Record
	Report history.Report
}

type Runner struct {
	config Config
}

func New(config Config) (*Runner, error) {
	if config.DatabaseURL == "" {
		return nil, errors.New("database URL is required")
	}
	if config.ScenarioPath == "" {
		return nil, errors.New("scenario path is required")
	}
	if config.Timeout <= 0 {
		config.Timeout = 2 * time.Minute
	}
	if config.WorkDir == "" {
		dir, err := os.MkdirTemp("", "orbit-scenario-")
		if err != nil {
			return nil, err
		}
		config.WorkDir = dir
	}
	if config.Binaries.Orbitd == "" || config.Binaries.Gateway == "" ||
		config.Binaries.Client == "" || config.Binaries.Orbitctl == "" {
		return nil, errors.New("orbitd, gateway, client, and orbitctl binaries are required")
	}
	return &Runner{config: config}, nil
}

func (r *Runner) Run(ctx context.Context) (Result, error) {
	scenarioFile, err := os.Open(r.config.ScenarioPath)
	if err != nil {
		return Result{}, err
	}
	defer scenarioFile.Close()
	document, err := scenario.Load(scenarioFile)
	if err != nil {
		return Result{}, err
	}
	schedule, err := scenario.CompileSchedule(document)
	if err != nil {
		return Result{}, err
	}
	scheduleJSON, err := scenario.CanonicalScheduleJSON(schedule)
	if err != nil {
		return Result{}, err
	}
	schedulePath := filepath.Join(r.config.WorkDir, "schedule.json")
	if err := os.WriteFile(schedulePath, []byte(scheduleJSON), 0o600); err != nil {
		return Result{}, err
	}

	pool, err := pgxpool.New(ctx, r.config.DatabaseURL)
	if err != nil {
		return Result{}, err
	}
	defer pool.Close()
	migrationDirectory, err := filepath.Abs(filepath.Join(filepath.Dir(r.config.ScenarioPath), "..", "..", "migrations"))
	if err != nil {
		return Result{}, err
	}
	if err := migrate.Apply(ctx, pool, migrationDirectory, migrate.Up, 0); err != nil {
		return Result{}, fmt.Errorf("apply migrations: %w", err)
	}
	if _, err := pool.Exec(ctx, `
		TRUNCATE orbit.audit_events, orbit.delivery_attempts, orbit.commands,
		         orbit.device_cursors RESTART IDENTITY CASCADE`); err != nil {
		return Result{}, fmt.Errorf("reset database: %w", err)
	}

	deviceID := document.Topology.Devices[0]
	controlAddress, err := reserveAddress()
	if err != nil {
		return Result{}, err
	}
	gatewayAddresses := make(map[string]string, len(document.Topology.Gateways))
	orderedGatewayAddresses := make([]string, 0, len(document.Topology.Gateways))
	for _, gatewayID := range document.Topology.Gateways {
		address, err := reserveAddress()
		if err != nil {
			return Result{}, err
		}
		gatewayAddresses[gatewayID] = address
		orderedGatewayAddresses = append(orderedGatewayAddresses, address)
	}
	statePath := filepath.Join(r.config.WorkDir, "client-state.json")
	clientLogPath := filepath.Join(r.config.WorkDir, "client.out.log")

	startedAt := time.Now().UTC()
	processes := &processGroup{}
	defer processes.stopAll()

	commonEnv := []string{
		"ORBIT_DATABASE_URL=" + r.config.DatabaseURL,
		"ORBIT_CONTROL_ADDRESS=" + controlAddress,
		"ORBIT_DEVICE_ID=" + deviceID,
		"ORBIT_CLIENT_GATEWAY_ADDRESSES=" + strings.Join(orderedGatewayAddresses, ","),
		"ORBIT_CLIENT_GATEWAY_ADDRESS=" + orderedGatewayAddresses[0],
		"ORBIT_CLIENT_GATEWAY_INDEX=0",
		"ORBIT_CLIENT_STATE_PATH=" + statePath,
		"ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS=5",
		"ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS=0",
		"ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY=50ms",
		"ORBIT_GATEWAY_RECONNECT_MAX_DELAY=1s",
		"ORBIT_GATEWAY_FAULT_SCHEDULE_PATH=" + schedulePath,
		"ORBIT_SCENARIO_STARTED_AT=" + startedAt.Format(time.RFC3339Nano),
	}

	if err := processes.start("orbitd", r.config.Binaries.Orbitd, append(commonEnv,
		"ORBIT_LISTEN_ADDRESS="+controlAddress,
	), r.config.WorkDir); err != nil {
		return Result{}, err
	}
	if err := waitHealthy(ctx, controlAddress, 20*time.Second); err != nil {
		return Result{}, err
	}
	for _, gatewayID := range document.Topology.Gateways {
		gatewayEnv := append(append([]string(nil), commonEnv...),
			"ORBIT_GATEWAY_ID="+gatewayID,
			"ORBIT_GATEWAY_LISTEN_ADDRESS="+gatewayAddresses[gatewayID],
		)
		if err := processes.start(gatewayID, r.config.Binaries.Gateway, gatewayEnv, r.config.WorkDir); err != nil {
			return Result{}, err
		}
		if err := waitHealthy(ctx, gatewayAddresses[gatewayID], 20*time.Second); err != nil {
			return Result{}, err
		}
	}
	if err := processes.start("client", r.config.Binaries.Client, commonEnv, r.config.WorkDir); err != nil {
		return Result{}, err
	}
	if err := waitForFile(ctx, statePath, 20*time.Second); err != nil {
		return Result{}, err
	}

	lifecycle := []history.LifecycleEvent{{
		AtMS:      0,
		Component: "runner",
		Action:    "started",
		Detail:    document.Name,
	}}

	runCtx, cancel := context.WithTimeout(ctx, r.config.Timeout)
	defer cancel()
	lifecycleDone := make(chan error, 1)
	go func() {
		lifecycleDone <- r.runLifecycle(runCtx, startedAt, schedule, document.Topology.Gateways, gatewayAddresses, deviceID, processes, &lifecycle)
	}()

	playbookErr := r.runPlaybook(runCtx, playbookContext{
		document:       document,
		controlAddress: controlAddress,
		deviceID:       deviceID,
	})

	lifecycleErr := <-lifecycleDone
	if playbookErr != nil {
		return Result{}, playbookErr
	}
	if lifecycleErr != nil && !errors.Is(lifecycleErr, context.Canceled) && !errors.Is(lifecycleErr, context.DeadlineExceeded) {
		return Result{}, lifecycleErr
	}

	applications, err := parseClientApplications(clientLogPath, deviceID)
	if err != nil {
		return Result{}, err
	}

	record, err := history.Collect(ctx, pool, history.Record{
		ScenarioName: document.Name,
		ScenarioSeed: document.Seed,
		Schedule:     schedule,
		StartedAt:    startedAt,
		Applications: applications,
		Lifecycle:    lifecycle,
	})
	if err != nil {
		return Result{}, err
	}
	report := history.Check(record)
	if err := writeArtifacts(r.config.WorkDir, record, report); err != nil {
		return Result{}, err
	}
	return Result{Record: record, Report: report}, nil
}

type playbookContext struct {
	document       scenario.Scenario
	controlAddress string
	deviceID       string
}

func (r *Runner) runPlaybook(ctx context.Context, playbook playbookContext) error {
	switch playbook.document.Name {
	case "online-smoke":
		_, err := submitAndWaitAcknowledged(ctx, r.config.Binaries.Orbitctl, playbook.controlAddress, playbook.deviceID, "online-smoke-1")
		return err
	case "offline-reconnect":
		deadline := time.Now().Add(r.config.Timeout)
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if _, err := submitAndWaitAcknowledged(ctx, r.config.Binaries.Orbitctl, playbook.controlAddress, playbook.deviceID, "offline-reconnect-1"); err == nil {
				return nil
			}
			time.Sleep(500 * time.Millisecond)
		}
		return errors.New("offline-reconnect playbook did not reach ACKNOWLEDGED")
	case "dual-gateway-session":
		if err := waitPlaybookDelay(ctx, 4*time.Second); err != nil {
			return err
		}
		_, err := submitAndWaitAcknowledged(ctx, r.config.Binaries.Orbitctl, playbook.controlAddress, playbook.deviceID, "dual-gateway-session-1")
		return err
	case "gateway-crash-before-send":
		if err := waitPlaybookDelay(ctx, 3*time.Second); err != nil {
			return err
		}
		_, err := submitAndWaitAcknowledged(ctx, r.config.Binaries.Orbitctl, playbook.controlAddress, playbook.deviceID, "gateway-crash-before-send-1")
		return err
	case "gateway-crash-after-send":
		_, err := submitAndWaitAcknowledged(ctx, r.config.Binaries.Orbitctl, playbook.controlAddress, playbook.deviceID, "gateway-crash-after-send-1")
		return err
	default:
		return fmt.Errorf("unsupported scenario playbook %q", playbook.document.Name)
	}
}

func waitPlaybookDelay(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (r *Runner) runLifecycle(
	ctx context.Context,
	startedAt time.Time,
	schedule scenario.Schedule,
	topologyGateways []string,
	gatewayAddresses map[string]string,
	deviceID string,
	processes *processGroup,
	lifecycle *[]history.LifecycleEvent,
) error {
	for _, event := range schedule.Events {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		waitUntil := startedAt.Add(time.Duration(event.AtMS) * time.Millisecond)
		if delay := time.Until(waitUntil); delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		switch event.Type {
		case "device_disconnect":
			processes.stop("client")
			*lifecycle = append(*lifecycle, history.LifecycleEvent{
				AtMS: event.AtMS, Component: "client", Action: "stopped", Detail: event.DeviceID,
			})
		case "device_reconnect":
			if processes.running("client") {
				continue
			}
			env := processes.envFor("client")
			if err := processes.start("client", r.config.Binaries.Client, env, r.config.WorkDir); err != nil {
				return err
			}
			*lifecycle = append(*lifecycle, history.LifecycleEvent{
				AtMS: event.AtMS, Component: "client", Action: "started", Detail: event.DeviceID,
			})
		case "device_gateway_switch":
			processes.stop("client")
			address, ok := gatewayAddresses[event.GatewayID]
			if !ok {
				return fmt.Errorf("unknown gateway %q", event.GatewayID)
			}
			index, err := gatewayIndex(topologyGateways, event.GatewayID)
			if err != nil {
				return err
			}
			env := setEnvVar(processes.envFor("client"), "ORBIT_CLIENT_GATEWAY_ADDRESS", address)
			env = setEnvVar(env, "ORBIT_CLIENT_GATEWAY_INDEX", strconv.Itoa(index))
			if err := processes.start("client", r.config.Binaries.Client, env, r.config.WorkDir); err != nil {
				return err
			}
			*lifecycle = append(*lifecycle, history.LifecycleEvent{
				AtMS: event.AtMS, Component: "client", Action: "gateway_switch", Detail: event.GatewayID,
			})
		case "gateway_crash":
			processes.stop(event.GatewayID)
			*lifecycle = append(*lifecycle, history.LifecycleEvent{
				AtMS: event.AtMS, Component: "gateway", Action: "stopped", Detail: event.GatewayID,
			})
		case "gateway_recover":
			if processes.running(event.GatewayID) {
				continue
			}
			env := processes.envFor(event.GatewayID)
			if err := processes.start(event.GatewayID, r.config.Binaries.Gateway, env, r.config.WorkDir); err != nil {
				return err
			}
			if err := waitHealthy(ctx, envListenAddress(env), 20*time.Second); err != nil {
				return err
			}
			*lifecycle = append(*lifecycle, history.LifecycleEvent{
				AtMS: event.AtMS, Component: "gateway", Action: "started", Detail: event.GatewayID,
			})
		case "delivery_drop", "delivery_duplicate", "ack_drop", "ack_duplicate", "transport_profile":
			// Applied inside the gateway fault controller.
		default:
			return fmt.Errorf("unsupported lifecycle event type %q", event.Type)
		}
	}
	return nil
}

func envListenAddress(env []string) string {
	for _, item := range env {
		if strings.HasPrefix(item, "ORBIT_GATEWAY_LISTEN_ADDRESS=") {
			return strings.TrimPrefix(item, "ORBIT_GATEWAY_LISTEN_ADDRESS=")
		}
	}
	return ""
}

type commandResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
}

func submitAndWaitAcknowledged(ctx context.Context, orbitctlBinary, controlAddress, deviceID, idempotencyKey string) (string, error) {
	submit := exec.CommandContext(ctx, orbitctlBinary,
		"submit",
		"-address", controlAddress,
		"-producer", "scenario-producer",
		"-idempotency-key", idempotencyKey,
		"-device", deviceID,
		"-priority", "4",
		"-payload", "scenario-command",
		"-expires-after", "1h",
	)
	submit.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	submittedOutput, err := submit.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("submit: %w\n%s", err, submittedOutput)
	}
	var submitted commandResponse
	if err := json.Unmarshal(bytes.TrimSpace(submittedOutput), &submitted); err != nil {
		return "", fmt.Errorf("decode submit response: %w", err)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		get := exec.CommandContext(ctx, orbitctlBinary,
			"get",
			"-address", controlAddress,
			"-command-id", submitted.CommandID,
		)
		get.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		currentOutput, err := get.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("get: %w\n%s", err, currentOutput)
		}
		var current commandResponse
		if err := json.Unmarshal(bytes.TrimSpace(currentOutput), &current); err != nil {
			return "", fmt.Errorf("decode get response: %w", err)
		}
		if current.State == "COMMAND_STATE_ACKNOWLEDGED" {
			return submitted.CommandID, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return "", fmt.Errorf("command %s did not reach ACKNOWLEDGED", submitted.CommandID)
}

func parseClientApplications(path, deviceID string) ([]history.ClientApplication, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()

	type logRecord struct {
		Message  string `json:"msg"`
		Command  string `json:"command_id"`
		Sequence int64  `json:"sequence_number"`
	}
	var applications []history.ClientApplication
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record logRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record.Message != "applying command" || record.Command == "" {
			continue
		}
		applications = append(applications, history.ClientApplication{
			CommandID:      record.Command,
			DeviceID:       deviceID,
			SequenceNumber: record.Sequence,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return applications, nil
}

func waitForFile(ctx context.Context, path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if _, err := os.Stat(path); err == nil {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("file %s was not created within %s", path, timeout)
}

func setEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	for index, item := range env {
		if strings.HasPrefix(item, prefix) {
			env[index] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

func gatewayIndex(topology []string, gatewayID string) (int, error) {
	for index, candidate := range topology {
		if candidate == gatewayID {
			return index, nil
		}
	}
	return 0, fmt.Errorf("unknown gateway %q", gatewayID)
}

func writeArtifacts(workDir string, record history.Record, report history.Report) error {
	if workDir == "" {
		return nil
	}
	historyBytes, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encode history artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "history.json"), historyBytes, 0o600); err != nil {
		return fmt.Errorf("write history artifact: %w", err)
	}
	reportBytes, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("encode checker artifact: %w", err)
	}
	if err := os.WriteFile(filepath.Join(workDir, "checker-report.json"), reportBytes, 0o600); err != nil {
		return fmt.Errorf("write checker artifact: %w", err)
	}
	return nil
}
