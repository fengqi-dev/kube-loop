//go:build darwin

package helperinstall

import (
	"fmt"
	"os/exec"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func prepareBinaryInstall() error {
	target := "system/" + helper.ServiceLabel()
	_ = exec.Command("launchctl", "bootout", target).Run()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := exec.Command("launchctl", "print", target).Run(); err != nil {
			//nolint:nilerr // launchctl failure here means the service is no longer registered.
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("launchd service %s is still running", helper.ServiceLabel())
}
