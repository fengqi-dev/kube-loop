//go:build darwin

package install

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/kballard/go-shellquote"
)

func ElevateInstall(ctx context.Context, source, expectedSHA256, token string, uid int, homeDir, singBoxPath string) error {
	command := `set -eu
workdir="$(mktemp -d "${TMPDIR:-/private/tmp}/kubeloop-helper.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
staged="$workdir/kubeloop-helper"
/bin/cp ` + shellquote.Join(source) + ` "$staged"
actual="$(/usr/bin/shasum -a 256 "$staged")"
actual="${actual%% *}"
if [ "$actual" != ` + shellquote.Join(expectedSHA256) + ` ]; then
	echo "bundled helper checksum mismatch" >&2
	exit 1
fi
/bin/chmod 700 "$staged"
"$staged" install --source "$staged" --token ` + shellquote.Join(token) +
		` --uid ` + shellquote.Join(strconv.Itoa(uid)) +
		` --version ` + shellquote.Join(helper.Version) +
		` --home ` + shellquote.Join(homeDir) +
		` --sing-box ` + shellquote.Join(singBoxPath)
	script := "do shell script " + strconv.Quote(command) +
		" with administrator privileges"
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("install helper: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func ElevateUninstall(ctx context.Context, source string) error {
	script := "do shell script " + strconv.Quote(shellquote.Join(source)+" uninstall") +
		" with administrator privileges"
	cmd := exec.CommandContext(ctx, "osascript", "-e", script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("uninstall helper: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
