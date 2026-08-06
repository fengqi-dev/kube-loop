//go:build !darwin && !linux && !windows

package terminal

import (
	"fmt"
	"runtime"
)

func open(string) error {
	return fmt.Errorf("opening a terminal is not supported on %s", runtime.GOOS)
}
