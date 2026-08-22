package fileapi

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"strconv"

	"github.com/kballard/go-shellquote"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func (executor *KubernetesTransferExecutor) Download(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, _ string,
	spec Spec,
	metadata func(DownloadMetadata) error,
	output io.Writer,
) (Outcome, error) {
	if metadata == nil || output == nil {
		return Outcome{}, errors.New(
			"download metadata callback and output are required",
		)
	}
	if spec.AllowedRoot == "" {
		return Outcome{}, errors.New("download allowed root is required")
	}
	if spec.Kind == KindFile {
		size, checksum, err := executor.inspectFile(
			ctx,
			identity,
			namespace,
			spec,
			spec.RemotePath,
		)
		if err != nil {
			return Outcome{}, err
		}
		if size > executor.maximumBytes || spec.Offset > size {
			return Outcome{}, errors.New(
				"download exceeds the configured size or offset limit",
			)
		}
		if err := metadata(DownloadMetadata{Total: size}); err != nil {
			return Outcome{}, err
		}
		script := "test ! -L " + shellquote.Join(
			spec.RemotePath,
		) + "; cat -- " + shellquote.Join(
			spec.RemotePath,
		)
		if spec.Offset > 0 {
			script = "test ! -L " + shellquote.Join(
				spec.RemotePath,
			) + "; tail -c +" + strconv.FormatUint(
				spec.Offset+1,
				10,
			) + " -- " + shellquote.Join(
				spec.RemotePath,
			)
		}
		if err := executor.shell(ctx, identity, namespace, spec, script, nil, output); err != nil {
			return Outcome{}, err
		}
		return Outcome{
			Transferred: size,
			Checksum:    checksum,
			HasChecksum: true,
		}, nil
	}
	if spec.Offset != 0 {
		return Outcome{}, errors.New(
			"directory download cannot resume from a byte offset",
		)
	}
	if err := metadata(DownloadMetadata{}); err != nil {
		return Outcome{}, err
	}
	reader, writer := io.Pipe()
	execResult := make(chan error, 1)
	go func() {
		script := "test ! -L " + shellquote.Join(
			spec.RemotePath,
		) + "; test -d " + shellquote.Join(
			spec.RemotePath,
		) +
			"; tar cf - -C " + shellquote.Join(
			spec.RemotePath,
		) + " ."
		err := executor.shell(
			ctx,
			identity,
			namespace,
			spec,
			script,
			nil,
			writer,
		)
		_ = writer.CloseWithError(err)
		execResult <- err
	}()
	hash := sha256.New()
	counter := &countingWriter{
		writer:  io.MultiWriter(output, hash),
		maximum: executor.maximumBytes,
	}
	sanitizeErr := sanitizeArchive(ctx, reader, counter)
	if sanitizeErr != nil {
		_ = reader.CloseWithError(sanitizeErr)
	}
	_, _ = io.Copy(io.Discard, reader)
	execErr := <-execResult
	if sanitizeErr != nil {
		return Outcome{}, sanitizeErr
	}
	if execErr != nil {
		return Outcome{}, execErr
	}
	var checksum [32]byte
	copy(checksum[:], hash.Sum(nil))
	return Outcome{
		Transferred: counter.written,
		Checksum:    checksum,
		HasChecksum: true,
	}, nil
}
