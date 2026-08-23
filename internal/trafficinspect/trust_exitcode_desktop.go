//go:build darwin || windows

package trafficinspect

import (
	"errors"
	"os/exec"
)

func exitCodeIs(err error, code int) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}
