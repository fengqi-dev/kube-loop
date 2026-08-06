//go:build darwin || linux

package platform

import "os"

func StopManagedProcess(process *os.Process) error {
	return process.Signal(os.Interrupt)
}
