//go:build !darwin && !linux && !windows

package install

import (
	"context"
	"fmt"
)

func ElevateInstall(context.Context, string, string, string, int, string, string) error {
	return fmt.Errorf("helper install is unsupported on this platform")
}

func ElevateUninstall(context.Context, string) error {
	return fmt.Errorf("helper uninstall is unsupported on this platform")
}
