//go:build darwin

package supervisor

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func Install(source, expectedSHA256, token string, uid int) error {
	config := CurrentConfig()
	if os.Geteuid() != 0 {
		return fmt.Errorf("supervisor install must run as root")
	}
	auth, err := NewAuth(token, uid)
	if err != nil {
		return err
	}
	if err := stopLaunchdService(config.ServiceLabel, 5*time.Second); err != nil {
		return err
	}
	if err := copyVerified(source, config.BinaryPath, expectedSHA256, 0o755); err != nil {
		return fmt.Errorf("install supervisor binary: %w", err)
	}
	if err := WriteAuth(config, auth); err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>
<string>%s</string><string>run</string><string>--channel</string><string>%s</string>
</array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, config.ServiceLabel, config.BinaryPath, config.Channel, config.LogPath, config.LogPath)
	//nolint:gosec // launchd property lists are intentionally system-readable and contain no secrets.
	if err := os.WriteFile(config.PlistPath(), []byte(plist), 0o644); err != nil {
		return fmt.Errorf("write supervisor launchd plist: %w", err)
	}
	//nolint:gosec // The plist path is derived from the fixed supervisor configuration.
	bootstrap := exec.Command("/bin/launchctl", "bootstrap", "system", config.PlistPath())
	if output, err := bootstrap.CombinedOutput(); err != nil {
		return fmt.Errorf("bootstrap supervisor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	//nolint:gosec // ServiceLabel is selected from fixed supervisor identifiers.
	_ = exec.Command("/bin/launchctl", "enable", "system/"+config.ServiceLabel).Run()
	//nolint:gosec // ServiceLabel is selected from fixed supervisor identifiers.
	kickstart := exec.Command("/bin/launchctl", "kickstart", "-k", "system/"+config.ServiceLabel)
	if output, err := kickstart.CombinedOutput(); err != nil {
		return fmt.Errorf("start supervisor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func copyVerified(source, destination, expectedSHA256 string, mode os.FileMode) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = input.Close() }()
	//nolint:gosec // The system executable directory must be traversable by launchd.
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(destination), ".kubeloop-supervisor-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(temp, hash), input); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != expectedSHA256 {
		return fmt.Errorf("SHA-256 mismatch")
	}
	if err := os.Rename(tempPath, destination); err != nil {
		return err
	}
	return syncDir(filepath.Dir(destination))
}

func stopLaunchdService(label string, timeout time.Duration) error {
	target := "system/" + label
	_ = exec.Command("/bin/launchctl", "bootout", target).Run()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := exec.Command("/bin/launchctl", "print", target).Run(); err != nil {
			//nolint:nilerr // launchctl failure here confirms the service is no longer registered.
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("launchd service %s is still running", label)
}
