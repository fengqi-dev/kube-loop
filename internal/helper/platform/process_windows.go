//go:build windows

package platform

import "os"

func StopManagedProcess(process *os.Process) error {
	return process.Kill()
}
