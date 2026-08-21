//go:build darwin

package install

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"
)

func ElevateSupervisorInstall(
	ctx context.Context,
	supervisorSource, supervisorSHA, workerSource, workerSHA, workerVersion, channel, token string,
	uid int,
	home, singBox, certificatePath string,
) error {
	command := darwinSupervisorInstallCommand(
		supervisorSource, supervisorSHA, workerSource, workerSHA,
		workerVersion, channel, token, uid, home, singBox, certificatePath,
	)
	script := "do shell script " + strconv.Quote(command) + " with administrator privileges"
	output, err := exec.CommandContext(ctx, "osascript", "-e", script).CombinedOutput()
	if err != nil {
		return fmt.Errorf("install supervisor: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func darwinSupervisorInstallCommand(
	supervisorSource, supervisorSHA, workerSource, workerSHA, workerVersion, channel, token string,
	uid int,
	home, singBox, certificatePath string,
) string {
	command := `set -eu
workdir="$(mktemp -d "${TMPDIR:-/private/tmp}/kubeloop-supervisor.XXXXXX")"
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
supervisor="$workdir/kubeloop-supervisor"
worker="$workdir/kubeloop-helper"
/bin/cp ` + shellquote.Join(supervisorSource) + ` "$supervisor"
/bin/cp ` + shellquote.Join(workerSource) + ` "$worker"
/bin/chmod 700 "$supervisor" "$worker"
supervisor_actual="$(/usr/bin/shasum -a 256 "$supervisor")"; supervisor_actual="${supervisor_actual%% *}"
worker_actual="$(/usr/bin/shasum -a 256 "$worker")"; worker_actual="${worker_actual%% *}"
[ "$supervisor_actual" = ` + shellquote.Join(supervisorSHA) + ` ] || { echo "supervisor checksum mismatch" >&2; exit 1; }
[ "$worker_actual" = ` + shellquote.Join(workerSHA) + ` ] || { echo "worker checksum mismatch" >&2; exit 1; }
"$supervisor" install --source "$supervisor" --sha256 ` + shellquote.Join(supervisorSHA) +
		` --worker "$worker" --worker-sha256 ` + shellquote.Join(workerSHA) +
		` --worker-version ` + shellquote.Join(workerVersion) +
		` --channel ` + shellquote.Join(channel) +
		` --token ` + shellquote.Join(token) +
		` --uid ` + shellquote.Join(strconv.Itoa(uid)) +
		` --home ` + shellquote.Join(home) +
		` --sing-box ` + shellquote.Join(singBox)
	if certificatePath != "" {
		command += "\n" + shellquote.Join(
			"/usr/bin/security", "add-trusted-cert", "-d", "-r", "trustRoot",
			"-k", "/Library/Keychains/System.keychain", certificatePath,
		)
	}
	return command
}
