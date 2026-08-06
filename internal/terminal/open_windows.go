//go:build windows

package terminal

import (
	"fmt"
	"os/exec"
	"syscall"
)

const createNewConsole = 0x00000010

func open(command string) error {
	path, err := exec.LookPath("powershell.exe")
	if err != nil {
		return fmt.Errorf("find PowerShell: %w", err)
	}
	process := exec.Command(path, "-NoExit", "-Command", command)
	process.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewConsole}
	if err := process.Start(); err != nil {
		return fmt.Errorf("open PowerShell: %w", err)
	}
	return process.Process.Release()
}
