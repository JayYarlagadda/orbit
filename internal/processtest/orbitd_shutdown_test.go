package processtest

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestOrbitdGracefulShutdown(t *testing.T) {
	databaseURL := requirePostgres(t)
	resetPostgres(t, databaseURL)

	listenAddress := freeAddress(t)
	cmd := exec.Command(orbitdBinary)
	cmd.Env = withTelemetryOff(append(os.Environ(),
		"ORBIT_DATABASE_URL="+databaseURL,
		"ORBIT_LISTEN_ADDRESS="+listenAddress,
		"ORBIT_SHUTDOWN_TIMEOUT=2s",
	))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	prepareInterruptible(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitHealthy(t, listenAddress, 20*time.Second)
	if err := interrupt(cmd); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitExit(t, cmd, 8*time.Second)
}
