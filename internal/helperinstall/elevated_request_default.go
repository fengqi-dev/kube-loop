//go:build !windows

package helperinstall

import "fmt"

func RunElevatedRequest(string, string, string) error {
	return fmt.Errorf("elevated helper requests are unsupported on this platform")
}
