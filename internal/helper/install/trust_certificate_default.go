//go:build !darwin

package install

import (
	"context"
	"errors"
)

func ManageDarwinTrustFromCLI(context.Context, string, string) error {
	return errors.New("macOS certificate trust is unavailable on this platform")
}
