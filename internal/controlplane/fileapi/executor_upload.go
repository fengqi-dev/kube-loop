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
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func (executor *KubernetesTransferExecutor) Upload(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace, taskID string,
	spec Spec,
	input io.Reader,
) (Outcome, error) {
	if input == nil {
		return Outcome{}, errors.New("upload input is required")
	}
	if spec.AllowedRoot == "" {
		return Outcome{}, errors.New("upload allowed root is required")
	}
	temporaryArchive := uploadTemporaryPath(spec, taskID)
	if spec.Kind == KindDirectory {
		if spec.Offset != 0 {
			return Outcome{}, errors.New(
				"directory upload cannot resume from a byte offset",
			)
		}
		if err := executor.uploadValidatedArchive(ctx, identity, namespace, spec, temporaryArchive, input); err != nil {
			executor.remove(ctx, identity, namespace, spec, temporaryArchive)
			return Outcome{}, err
		}
	} else if err := executor.uploadFile(ctx, identity, namespace, spec, temporaryArchive, input); err != nil {
		return Outcome{}, err
	}
	size, checksum, err := executor.inspectFile(
		ctx,
		identity,
		namespace,
		spec,
		temporaryArchive,
	)
	if err != nil {
		return Outcome{}, err
	}
	expected, _ := hex.DecodeString(spec.Checksum)
	if size != spec.Size || !bytes.Equal(checksum[:], expected) {
		// The caller sanitizes this before the client sees it, so name what
		// actually differs: a short write and corrupted content are the same
		// message otherwise, and a resume can only be debugged by telling them
		// apart.
		return Outcome{}, fmt.Errorf(
			"uploaded content does not match declared size and checksum: "+
				"size %d of %d, checksum %s of %s, resumed from %d",
			size, spec.Size, hex.EncodeToString(checksum[:]), spec.Checksum, spec.Offset,
		)
	}
	finalSource := temporaryArchive
	cleanupSource := ""
	if spec.Kind == KindDirectory {
		temporaryDirectory := spec.RemotePath + ".kubeloop-" + taskID + ".dir"
		script := "set -eu; rm -rf -- " + shellquote.Join(temporaryDirectory) +
			"; mkdir -p -- " + shellquote.Join(temporaryDirectory) +
			"; tar xf " + shellquote.Join(temporaryArchive) + " -C " + shellquote.Join(temporaryDirectory)
		if err := executor.shell(ctx, identity, namespace, spec, script, nil, io.Discard); err != nil {
			executor.remove(ctx, identity, namespace, spec, temporaryDirectory)
			return Outcome{}, err
		}
		finalSource = temporaryDirectory
		cleanupSource = temporaryArchive
	}
	if err := executor.finalize(ctx, identity, namespace, spec, finalSource); err != nil {
		return Outcome{}, err
	}
	if cleanupSource != "" {
		executor.remove(ctx, identity, namespace, spec, cleanupSource)
	}
	return Outcome{
		Transferred: spec.Size,
		Checksum:    checksum,
		HasChecksum: true,
	}, nil
}

func (executor *KubernetesTransferExecutor) UploadOffset(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
) (uint64, error) {
	if spec.ResumeID == "" || spec.Kind != KindFile ||
		spec.Direction != DirectionUpload ||
		spec.AllowedRoot == "" {
		return 0, errors.New("resumable file upload specification is invalid")
	}
	temporary := uploadTemporaryPath(spec, "")
	// The interrupted attempt's container shell exits on its own schedule: the
	// Control Plane stops a transfer as soon as its own stream ends, but the
	// `cat` still appending inside the container only sees the closed stdin a
	// moment later. Reporting a size while that writer is alive hands back an
	// offset the resumed upload then has to race, and both writers end up
	// appending to the same partial file. Report the size only once two
	// consecutive reads agree, so the resume starts after the old writer
	// rather than alongside it.
	settled, err := executor.partialUploadSize(ctx, identity, namespace, spec, temporary)
	if err != nil {
		return 0, err
	}
	for range partialUploadSettleReads {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(partialUploadSettleInterval):
		}
		current, err := executor.partialUploadSize(ctx, identity, namespace, spec, temporary)
		if err != nil {
			return 0, err
		}
		if current == settled {
			return settled, nil
		}
		settled = current
	}
	// A partial file that never settles is still resumable: the upload
	// reconciles a larger one by trimming it back to this offset.
	return settled, nil
}

// partialUploadSize reports how much of a resumable upload the container
// already holds. A symlink or any other non-regular file at the partial path
// is refused rather than measured, so a resume cannot be redirected.
func (executor *KubernetesTransferExecutor) partialUploadSize(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	temporary string,
) (uint64, error) {
	var stdout bytes.Buffer
	quoted := shellquote.Join(temporary)
	script := "if [ -L " + quoted + " ]; then exit 74; " +
		"elif [ -f " + quoted + " ]; then wc -c < " + quoted + "; " +
		"elif [ -e " + quoted + " ]; then exit 75; else echo 0; fi"
	if err := executor.shell(ctx, identity, namespace, spec, script, nil, &stdout); err != nil {
		return 0, err
	}
	size, err := strconv.ParseUint(strings.TrimSpace(stdout.String()), 10, 64)
	if err != nil || size > executor.maximumBytes {
		return 0, errors.New(
			"container returned an invalid partial upload size",
		)
	}
	return size, nil
}

func uploadTemporaryPath(spec Spec, taskID string) string {
	id := taskID
	if spec.ResumeID != "" {
		id = spec.ResumeID
	}
	return spec.RemotePath + ".kubeloop-" + id + ".part"
}
func (executor *KubernetesTransferExecutor) uploadFile(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	temporary string,
	input io.Reader,
) error {
	parent := path.Dir(temporary)
	operator := ">"
	precondition := ""
	if spec.Offset > 0 {
		operator = ">>"
		quotedTemporary := shellquote.Join(temporary)
		quotedTrimmed := shellquote.Join(temporary + ".trim")
		offset := strconv.FormatUint(spec.Offset, 10)
		precondition = "actual=$(wc -c < " + quotedTemporary + "); " +
			"test \"$actual\" -ge " + offset + "; " +
			"rm -f -- " + quotedTrimmed + "; " +
			"head -c " + offset + " -- " + quotedTemporary + " > " + quotedTrimmed + "; " +
			"mv -f -- " + quotedTrimmed + " " + quotedTemporary + "; "
	}
	script := "set -eu; mkdir -p -- " + shellquote.Join(
		parent,
	) + "; " + precondition +
		"cat " + operator + " " + shellquote.Join(
		temporary,
	)
	return executor.shell(
		ctx,
		identity,
		namespace,
		spec,
		script,
		input,
		io.Discard,
	)
}
