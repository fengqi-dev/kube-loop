//go:build !darwin && !linux && !windows

package helperinstall

import "fmt"

func enableService(string) error {
	return fmt.Errorf("helper service is unsupported on this platform")
}

func disableService() error {
	return fmt.Errorf("helper service is unsupported on this platform")
}
