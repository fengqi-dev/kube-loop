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
if [ -n "$8" ]; then
	trust_backend=""
	update_command=""
	if command -v update-ca-certificates >/dev/null 2>&1; then
		trust_backend="debian"
		update_command="$(command -v update-ca-certificates)"
	elif [ -x /usr/sbin/update-ca-certificates ]; then
		trust_backend="debian"
		update_command="/usr/sbin/update-ca-certificates"
	elif [ -x /usr/bin/update-ca-certificates ]; then
		trust_backend="debian"
		update_command="/usr/bin/update-ca-certificates"
	elif command -v update-ca-trust >/dev/null 2>&1; then
		trust_backend="redhat"
		update_command="$(command -v update-ca-trust)"
	elif [ -x /usr/bin/update-ca-trust ]; then
		trust_backend="redhat"
		update_command="/usr/bin/update-ca-trust"
	elif [ -x /usr/sbin/update-ca-trust ]; then
		trust_backend="redhat"
		update_command="/usr/sbin/update-ca-trust"
	fi
	if [ "$trust_backend" = "debian" ]; then
		anchor="/usr/local/share/ca-certificates/kubeloop-traffic-inspection.crt"
		install -d -m 0755 -- "$(dirname "$anchor")"
		install -m 0644 -- "$8" "$anchor"
		"$update_command"
	elif [ "$trust_backend" = "redhat" ]; then
		anchor="/etc/pki/ca-trust/source/anchors/kubeloop-traffic-inspection.pem"
		install -d -m 0755 -- "$(dirname "$anchor")"
		install -m 0644 -- "$8" "$anchor"
		"$update_command" extract
	else
		echo "Linux system trust update command not found" >&2
		exit 1
	fi
fi
`

func ElevateInstall(ctx context.Context, source, expectedSHA256, token string, uid int, homeDir, singBoxPath string) error {
	return elevateLinuxInstall(ctx, source, expectedSHA256, token, uid, homeDir, singBoxPath, "")
}

func elevateLinuxInstall(
	ctx context.Context,
	source, expectedSHA256, token string,
	uid int,
	homeDir, singBoxPath, certificatePath string,
) error {
	elevate := "pkexec"
	if _, err := exec.LookPath("pkexec"); err != nil {
		elevate = "sudo"
	}
	cmdArgs := []string{
		"/bin/sh", "-c", linuxInstallScript, "kubeloop-installer",
		source, expectedSHA256, token, strconv.Itoa(uid), helper.Version, homeDir, singBoxPath, certificatePath,
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

func ElevateUninstallWithCertificate(ctx context.Context, source, _ string) error {
	return ElevateUninstall(ctx, source)
}
