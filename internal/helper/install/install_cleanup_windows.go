//go:build windows

package install

import (
	"os"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func cleanupDisplacedHelperBinaries(current string) {
	for _, path := range helper.WindowsDisplacedHelperPaths(current) {
		_ = os.Remove(path)
	}
}
