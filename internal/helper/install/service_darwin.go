//go:build darwin

package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func launchdPlistPath() string {
	return "/Library/LaunchDaemons/" + helper.ServiceLabel() + ".plist"
}

func enableService(binaryPath string) error {
	label := helper.ServiceLabel()
	plistPath := launchdPlistPath()
	logPath := helper.HelperLogPath()
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string>
    <string>run</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`, label, binaryPath, logPath, logPath)
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(plist), 0o644); err != nil {
		return err
	}
	_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
	cmd := exec.Command("launchctl", "bootstrap", "system", plistPath)
	if _, err := cmd.CombinedOutput(); err != nil {
		// Older macOS fallback.
		cmd = exec.Command("launchctl", "load", "-w", plistPath)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("launchctl load helper: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	_ = exec.Command("launchctl", "enable", "system/"+label).Run()
	_ = exec.Command("launchctl", "kickstart", "-k", "system/"+label).Run()
	return nil
}

func disableService() error {
	label := helper.ServiceLabel()
	plistPath := launchdPlistPath()
	_ = exec.Command("launchctl", "bootout", "system/"+label).Run()
	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	_ = os.Remove(plistPath)
	return nil
}
