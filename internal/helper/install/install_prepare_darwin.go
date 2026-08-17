//go:build darwin

package install

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
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("launchd service %s is still running", helper.ServiceLabel())
}
