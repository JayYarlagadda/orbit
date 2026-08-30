package processtest

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type commandResponse struct {
	CommandID string `json:"command_id"`
	State     string `json:"state"`
}

func TestOnlineSmokePathWithOrbitdRestart(t *testing.T) {
	databaseURL := requirePostgres(t)
	resetPostgres(t, databaseURL)

	runDirectory := t.TempDir()
	statePath := filepath.Join(runDirectory, "client-state.json")

	controlAddress := freeAddress(t)
	gatewayAddress := freeAddress(t)

	startProcess := func(t *testing.T, name, binary string, env []string) *exec.Cmd {
		t.Helper()
		cmd := exec.Command(binary)
		cmd.Env = append(os.Environ(), env...)
		stdoutPath := filepath.Join(runDirectory, name+".out.log")
		stderrPath := filepath.Join(runDirectory, name+".err.log")
		stdout, err := os.Create(stdoutPath)
		if err != nil {
			t.Fatal(err)
		}
		stderr, err := os.Create(stderrPath)
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stdout = stdout
		cmd.Stderr = stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = stdout.Close()
			_ = stderr.Close()
			if cmd.Process != nil {
				_ = cmd.Process.Kill()
			}
		})
		return cmd
	}

	commonEnv := []string{
		"ORBIT_DATABASE_URL=" + databaseURL,
		"ORBIT_GATEWAY_ID=gateway-smoke",
		"ORBIT_CONTROL_ADDRESS=" + controlAddress,
		"ORBIT_GATEWAY_LISTEN_ADDRESS=" + gatewayAddress,
		"ORBIT_DEVICE_ID=edge-smoke",
		"ORBIT_CLIENT_GATEWAY_ADDRESS=" + gatewayAddress,
		"ORBIT_CLIENT_STATE_PATH=" + statePath,
		"ORBIT_CLIENT_MAX_RECONNECT_ATTEMPTS=5",
		"ORBIT_GATEWAY_MAX_RECONNECT_ATTEMPTS=0",
		"ORBIT_GATEWAY_RECONNECT_INITIAL_DELAY=50ms",
		"ORBIT_GATEWAY_RECONNECT_MAX_DELAY=1s",
	}

	orbitd := startProcess(t, "orbitd", orbitdBinary, append(commonEnv,
		"ORBIT_LISTEN_ADDRESS="+controlAddress,
	))
	waitHealthy(t, controlAddress, 20*time.Second)

	startProcess(t, "gateway", gatewayBinary, commonEnv)
	waitHealthy(t, gatewayAddress, 20*time.Second)

	startProcess(t, "client", clientBinary, commonEnv)
	waitForFile(t, statePath, 20*time.Second)

	firstID := submitAndWaitAcknowledged(t, orbitctlBinary, controlAddress, "smoke-first")
	t.Logf("first command %s acknowledged", firstID)

	if err := orbitd.Process.Kill(); err != nil {
		t.Fatalf("stop orbitd: %v", err)
	}
	waitAfterKill(t, orbitd, 10*time.Second)

	restarted := startProcess(t, "orbitd-restart", orbitdBinary, append(commonEnv,
		"ORBIT_LISTEN_ADDRESS="+controlAddress,
	))
	waitHealthy(t, controlAddress, 20*time.Second)

	time.Sleep(2 * time.Second)
	secondID := submitAndWaitAcknowledged(t, orbitctlBinary, controlAddress, "smoke-restart")
	t.Logf("second command %s acknowledged after orbitd restart", secondID)

	if restarted.ProcessState != nil && restarted.ProcessState.Exited() {
		t.Fatal("restarted orbitd exited before the second command acknowledged")
	}
}

func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("file %s was not created within %s", path, timeout)
}

func submitAndWaitAcknowledged(t *testing.T, orbitctlBinary, controlAddress, idempotencyKey string) string {
	t.Helper()
	submit := exec.Command(orbitctlBinary,
		"submit",
		"-address", controlAddress,
		"-producer", "smoke-producer",
		"-idempotency-key", idempotencyKey,
		"-device", "edge-smoke",
		"-priority", "4",
		"-payload", "collect-diagnostics",
		"-expires-after", "1h",
	)
	submit.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	submittedOutput, err := submit.CombinedOutput()
	if err != nil {
		t.Fatalf("submit: %v\n%s", err, submittedOutput)
	}
	var submitted commandResponse
	if err := json.Unmarshal(bytes.TrimSpace(submittedOutput), &submitted); err != nil {
		t.Fatalf("decode submit response: %v\n%s", err, submittedOutput)
	}
	if submitted.CommandID == "" {
		t.Fatalf("submit returned empty command_id: %s", submittedOutput)
	}

	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		get := exec.Command(orbitctlBinary,
			"get",
			"-address", controlAddress,
			"-command-id", submitted.CommandID,
		)
		get.Env = append(os.Environ(), "GOTOOLCHAIN=local")
		currentOutput, err := get.CombinedOutput()
		if err != nil {
			t.Fatalf("get: %v\n%s", err, currentOutput)
		}
		var current commandResponse
		if err := json.Unmarshal(bytes.TrimSpace(currentOutput), &current); err != nil {
			t.Fatalf("decode get response: %v\n%s", err, currentOutput)
		}
		if current.State == "COMMAND_STATE_ACKNOWLEDGED" {
			return submitted.CommandID
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("command %s did not reach ACKNOWLEDGED within the deadline", submitted.CommandID)
	return ""
}

func TestOnlineSmokeScriptDocumentsRestart(t *testing.T) {
	scriptPath := filepath.Join("..", "..", "scripts", "smoke-online.ps1")
	content, err := os.ReadFile(scriptPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(content)
	for _, fragment := range []string{
		"restarting orbitd",
		"ACKNOWLEDGED after orbitd restart",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("smoke script missing %q", fragment)
		}
	}
}
