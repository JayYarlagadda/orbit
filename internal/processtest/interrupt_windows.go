//go:build windows

package processtest

import (
	"os/exec"
	"syscall"
)

const createNewProcessGroup = 0x00000200

func prepareInterruptible(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

func interrupt(cmd *exec.Cmd) error {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	generate := kernel32.NewProc("GenerateConsoleCtrlEvent")
	r1, _, err := generate.Call(1, uintptr(cmd.Process.Pid)) // CTRL_BREAK_EVENT
	if r1 == 0 {
		return err
	}
	return nil
}
