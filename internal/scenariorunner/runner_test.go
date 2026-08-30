package scenariorunner

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRunnerOfflineReconnect(t *testing.T) {
	databaseURL := os.Getenv("ORBIT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORBIT_TEST_DATABASE_URL is not set")
	}
	binDir := t.TempDir()
	binaries, err := buildScenarioBinaries(binDir)
	if err != nil {
		t.Fatal(err)
	}
	scenarioPath := filepath.Join("..", "..", "scenarios", "examples", "offline-reconnect.v1.json")
	runner, err := New(Config{
		DatabaseURL:  databaseURL,
		ScenarioPath: scenarioPath,
		WorkDir:      t.TempDir(),
		Binaries:     binaries,
		Timeout:      3 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Passed {
		t.Fatalf("history checker failed: %+v", result.Report.Violations)
	}
}

func TestRunnerOnlineSmoke(t *testing.T) {
	databaseURL := os.Getenv("ORBIT_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ORBIT_TEST_DATABASE_URL is not set")
	}
	binDir := t.TempDir()
	binaries, err := buildScenarioBinaries(binDir)
	if err != nil {
		t.Fatal(err)
	}
	scenarioPath := filepath.Join("..", "..", "scenarios", "examples", "online-smoke.v1.json")
	runner, err := New(Config{
		DatabaseURL:  databaseURL,
		ScenarioPath: scenarioPath,
		WorkDir:      t.TempDir(),
		Binaries:     binaries,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := runner.Run(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !result.Report.Passed {
		t.Fatalf("history checker failed: %+v", result.Report.Violations)
	}
	foundAck := false
	for _, command := range result.Record.Commands {
		if command.State == "ACKNOWLEDGED" {
			foundAck = true
			break
		}
	}
	if !foundAck {
		t.Fatal("expected at least one acknowledged command")
	}
}

func buildScenarioBinaries(dir string) (Binaries, error) {
	orbitd, err := buildBinary(dir, "./cmd/orbitd", "orbitd")
	if err != nil {
		return Binaries{}, err
	}
	gateway, err := buildBinary(dir, "./cmd/gateway", "gateway")
	if err != nil {
		return Binaries{}, err
	}
	client, err := buildBinary(dir, "./cmd/client", "client")
	if err != nil {
		return Binaries{}, err
	}
	orbitctl, err := buildBinary(dir, "./cmd/orbitctl", "orbitctl")
	if err != nil {
		return Binaries{}, err
	}
	return Binaries{
		Orbitd:   orbitd,
		Gateway:  gateway,
		Client:   client,
		Orbitctl: orbitctl,
	}, nil
}

func buildBinary(dir, packagePath, name string) (string, error) {
	output := filepath.Join(dir, name)
	if os.PathSeparator == '\\' {
		output += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", output, packagePath)
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", err
	}
	_ = out
	return output, nil
}
