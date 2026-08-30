//go:build unix

package processtest

import (
	"os/exec"
	"syscall"
)

func prepareInterruptible(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func interrupt(cmd *exec.Cmd) error {
	return syscall.Kill(cmd.Process.Pid, syscall.SIGTERM)
}
