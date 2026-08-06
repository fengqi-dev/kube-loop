//go:build e2e && windows

package platform

import (
	"os/exec"
	"strings"
	"testing"
)

func assertPlatformE2EDNS(t *testing.T, want bool) {
	t.Helper()
	command := `(Get-DnsClientNrptRule -ErrorAction SilentlyContinue | ` +
		`Where-Object { $_.Comment -eq 'KubeLoop' -and $_.Namespace -contains '.` +
		platformE2EDomain + `' }).Count`
	output, err := exec.Command(
		"powershell.exe", "-NoProfile", "-NonInteractive", "-Command", command,
	).CombinedOutput()
	got := err == nil && strings.TrimSpace(string(output)) != "0"
	if got != want {
		t.Fatalf(
			"Windows NRPT configured=%v want %v (output=%q error=%v)",
			got, want, strings.TrimSpace(string(output)), err,
		)
	}
}
