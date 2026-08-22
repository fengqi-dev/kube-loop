package fileapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path"
	"strconv"
	"strings"

	"github.com/kballard/go-shellquote"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
)

func (executor *KubernetesTransferExecutor) inspectFile(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	filename string,
) (uint64, [32]byte, error) {
	var stdout bytes.Buffer
	script := "test ! -L " + shellquote.Join(
		filename,
	) + "; test -f " + shellquote.Join(
		filename,
	) + "; wc -c < " + shellquote.Join(
		filename,
	) +
		"; sha256sum -- " + shellquote.Join(
		filename,
	)
	if err := executor.shell(ctx, identity, namespace, spec, script, nil, &stdout); err != nil {
		return 0, [32]byte{}, err
	}
	lines := strings.Split(strings.TrimSpace(stdout.String()), "\n")
	if len(lines) != 2 {
		return 0, [32]byte{}, errors.New(
			"container returned invalid file metadata",
		)
	}
	size, err := strconv.ParseUint(strings.TrimSpace(lines[0]), 10, 64)
	if err != nil {
		return 0, [32]byte{}, errors.New("container returned invalid file size")
	}
	fields := strings.Fields(lines[1])
	if len(fields) < 1 {
		return 0, [32]byte{}, errors.New(
			"container returned invalid file checksum",
		)
	}
	decoded, err := hex.DecodeString(fields[0])
	if err != nil || len(decoded) != 32 {
		return 0, [32]byte{}, errors.New(
			"container returned invalid file checksum",
		)
	}
	var checksum [32]byte
	copy(checksum[:], decoded)
	return size, checksum, nil
}

func (executor *KubernetesTransferExecutor) finalize(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	temporary string,
) error {
	script := "set -eu; test ! -e " + shellquote.Join(
		spec.RemotePath,
	) + "; mv -- " + shellquote.Join(
		temporary,
	) + " " + shellquote.Join(
		spec.RemotePath,
	)
	if spec.Overwrite {
		script = "set -eu; rm -rf -- " + shellquote.Join(
			spec.RemotePath,
		) + "; mv -- " + shellquote.Join(
			temporary,
		) + " " + shellquote.Join(
			spec.RemotePath,
		)
	}
	return executor.shell(
		ctx,
		identity,
		namespace,
		spec,
		script,
		nil,
		io.Discard,
	)
}

func (executor *KubernetesTransferExecutor) remove(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	filename string,
) {
	_ = executor.shell(
		ctx,
		identity,
		namespace,
		spec,
		"rm -rf -- "+shellquote.Join(filename),
		nil,
		io.Discard,
	)
}

func (executor *KubernetesTransferExecutor) shell(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	script string,
	stdin io.Reader,
	stdout io.Writer,
) error {
	guardTarget := path.Dir(spec.RemotePath)
	if spec.Direction == DirectionDownload && spec.Kind == KindDirectory {
		guardTarget = spec.RemotePath
	}
	if spec.AllowedRoot == "" {
		return errors.New("container file operation has no allowed root")
	}
	script = physicalPathGuard(spec.AllowedRoot, guardTarget) + script
	stderr := &limitedBuffer{maximum: 4096}
	err := executor.pods.Exec(ctx, identity, namespace, execapi.Spec{
		Pod: spec.Pod, Container: spec.Container, Command: []string{"/bin/sh", "-c", script},
	}, execapi.Streams{Stdin: stdin, Stdout: stdout, Stderr: stderr})
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if message != "" {
			return fmt.Errorf(
				"container file operation failed: %w: %s",
				err,
				message,
			)
		}
		return fmt.Errorf("container file operation failed: %w", err)
	}
	return nil
}
