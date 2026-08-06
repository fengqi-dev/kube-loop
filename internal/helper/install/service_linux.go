//go:build linux

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func systemdUnitPath() string {
	return filepath.Join("/etc/systemd/system", helper.SystemdUnitName())
}

func enableService(binaryPath string) error {
	unitPath := systemdUnitPath()
	unitName := helper.SystemdUnitName()
	unit := fmt.Sprintf(`[Unit]
Description=%s
After=network.target

[Service]
Type=simple
ExecStart=%s run
Restart=on-failure
RestartSec=2

[Install]
WantedBy=multi-user.target
`, helper.ServiceDisplayName(), binaryPath)
	if err := os.WriteFile(unitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	commands := [][]string{
		{"systemctl", "daemon-reload"},
		{"systemctl", "enable", "--now", unitName},
	}
	for _, args := range commands {
		cmd := exec.Command(args[0], args[1:]...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func disableService() error {
	unitName := helper.SystemdUnitName()
	unitPath := systemdUnitPath()
	_ = exec.Command("systemctl", "disable", "--now", unitName).Run()
	_ = os.Remove(unitPath)
	_ = exec.Command("systemctl", "daemon-reload").Run()
	return nil
}
