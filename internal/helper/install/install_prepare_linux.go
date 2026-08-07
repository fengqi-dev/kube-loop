//go:build linux

package install

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func prepareBinaryInstall() error {
	unit := helper.SystemdUnitName()
	output, err := exec.Command("systemctl", "stop", unit).CombinedOutput()
	if err == nil {
		return nil
	}
	if activeErr := exec.Command("systemctl", "is-active", "--quiet", unit).Run(); activeErr != nil {
		return nil
	}
	return fmt.Errorf("systemctl stop %s: %w: %s", unit, err, strings.TrimSpace(string(output)))
}
