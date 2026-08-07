//go:build darwin

package install

import (
	"fmt"
	"os/exec"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func prepareBinaryInstall() error {
	target := "system/" + helper.ServiceLabel()
	_ = exec.Command("launchctl", "bootout", target).Run()
	if err := exec.Command("launchctl", "print", target).Run(); err == nil {
		return fmt.Errorf("launchd service %s is still running", helper.ServiceLabel())
	}
	return nil
}
