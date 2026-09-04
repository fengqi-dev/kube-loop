//go:build linux

package helperinstall

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

const systemctlCommand = "systemctl"

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
	if err := os.WriteFile(unitPath, []byte(unit), 0o600); err != nil {
		return err
	}
	if err := os.Chmod(unitPath, 0o600); err != nil {
		return fmt.Errorf("secure systemd unit: %w", err)
	}
	commands := [][]string{
		{"daemon-reload"},
		{"enable", unitName},
		{"restart", unitName},
	}
	for _, args := range commands {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, systemctlCommand, args...)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			return fmt.Errorf(
				"%s %s: %w: %s",
				systemctlCommand,
				strings.Join(args, " "),
				err,
				strings.TrimSpace(string(output)),
			)
		}
	}
	return nil
}

func disableService() error {
	unitName := helper.SystemdUnitName()
	unitPath := systemdUnitPath()
	_ = exec.Command(systemctlCommand, "disable", "--now", unitName).Run()
	_ = os.Remove(unitPath)
	_ = exec.Command(systemctlCommand, "daemon-reload").Run()
	return nil
}
