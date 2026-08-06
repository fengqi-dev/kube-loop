//go:build linux

package install

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

const linuxInstallScript = `set -eu
workdir="$(mktemp -d "${TMPDIR:-/tmp}/kubeloop-helper.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
staged="$workdir/kubeloop-helper"
cp -- "$1" "$staged"
actual="$(sha256sum "$staged")"
actual="${actual%% *}"
if [ "$actual" != "$2" ]; then
	echo "bundled helper checksum mismatch" >&2
	exit 1
fi
chmod 700 "$staged"
"$staged" install --source "$staged" --token "$3" --uid "$4" --version "$5" --home "$6" --sing-box "$7"
`

func ElevateInstall(ctx context.Context, source, expectedSHA256, token string, uid int, homeDir, singBoxPath string) error {
	elevate := "pkexec"
	if _, err := exec.LookPath("pkexec"); err != nil {
		elevate = "sudo"
	}
	cmdArgs := []string{
		"/bin/sh", "-c", linuxInstallScript, "kubeloop-installer",
		source, expectedSHA256, token, strconv.Itoa(uid), helper.Version, homeDir, singBoxPath,
	}
	cmd := exec.CommandContext(ctx, elevate, cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install helper (%s): %w: %s", elevate, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ElevateUninstall(ctx context.Context, source string) error {
	elevate := "pkexec"
	if _, err := exec.LookPath("pkexec"); err != nil {
		elevate = "sudo"
	}
	cmd := exec.CommandContext(ctx, elevate, source, "uninstall")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uninstall helper (%s): %w: %s", elevate, err, strings.TrimSpace(string(output)))
	}
	return nil
}
