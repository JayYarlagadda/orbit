package scenariorunner

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type managedProcess struct {
	cmd    *exec.Cmd
	env    []string
	stdout *os.File
	stderr *os.File
}

type processGroup struct {
	mu        sync.Mutex
	processes map[string]*managedProcess
}

func (g *processGroup) start(name, binary string, env []string, workDir string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.processes == nil {
		g.processes = make(map[string]*managedProcess)
	}
	if existing, ok := g.processes[name]; ok && existing.cmd != nil && existing.cmd.Process != nil && existing.cmd.ProcessState == nil {
		return fmt.Errorf("process %s is already running", name)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), env...)
	stdoutPath := fmt.Sprintf("%s/%s.out.log", workDir, name)
	stderrPath := fmt.Sprintf("%s/%s.err.log", workDir, name)
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		return err
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		_ = stdout.Close()
		return err
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		_ = stderr.Close()
		return err
	}
	g.processes[name] = &managedProcess{
		cmd:    cmd,
		env:    append([]string(nil), env...),
		stdout: stdout,
		stderr: stderr,
	}
	return nil
}

func (g *processGroup) envFor(name string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if process, ok := g.processes[name]; ok {
		return append([]string(nil), process.env...)
	}
	return nil
}

func (g *processGroup) running(name string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	process, ok := g.processes[name]
	return ok && process.cmd != nil && process.cmd.Process != nil && process.cmd.ProcessState == nil
}

func (g *processGroup) stop(name string) {
	g.mu.Lock()
	process, ok := g.processes[name]
	g.mu.Unlock()
	if !ok || process.cmd == nil || process.cmd.Process == nil {
		return
	}
	if process.stdout != nil {
		_ = process.stdout.Close()
		process.stdout = nil
	}
	if process.stderr != nil {
		_ = process.stderr.Close()
		process.stderr = nil
	}
	_ = process.cmd.Process.Kill()
	waitDone := make(chan struct{})
	go func() {
		_ = process.cmd.Wait()
		close(waitDone)
	}()
	select {
	case <-waitDone:
	case <-time.After(5 * time.Second):
	}
	process.cmd = nil
}

func (g *processGroup) stopAll() {
	g.mu.Lock()
	names := make([]string, 0, len(g.processes))
	for name := range g.processes {
		names = append(names, name)
	}
	g.mu.Unlock()
	for _, name := range names {
		g.stop(name)
	}
}

func waitHealthy(ctx context.Context, address string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		connection, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}
		checkCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		response, err := grpc_health_v1.NewHealthClient(connection).Check(checkCtx, &grpc_health_v1.HealthCheckRequest{})
		cancel()
		_ = connection.Close()
		if err == nil && response.GetStatus() == grpc_health_v1.HealthCheckResponse_SERVING {
			return nil
		}
		last = err
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("process at %s never became healthy: %v", address, last)
}

func reserveAddress() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	address := listener.Addr().String()
	_ = listener.Close()
	return address, nil
}
