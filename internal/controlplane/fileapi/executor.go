package fileapi

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
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
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
)

const maximumArchiveEntries = 100_000

type Outcome struct {
	Transferred uint64
	Checksum    [32]byte
	HasChecksum bool
}

type DownloadMetadata struct{ Total uint64 }

type TransferExecutor interface {
	Upload(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		Spec,
		io.Reader,
	) (Outcome, error)
	Download(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
		Spec,
		func(DownloadMetadata) error,
		io.Writer,
	) (Outcome, error)
	UploadOffset(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) (uint64, error)
}

type PodExecutor interface {
	Exec(
		context.Context,
		controlplaneapi.Identity,
		string,
		execapi.Spec,
		execapi.Streams,
	) error
}

type KubernetesTransferExecutor struct {
	pods         PodExecutor
	maximumBytes uint64
}

func NewKubernetesTransferExecutor(
	pods PodExecutor,
	maximumBytes uint64,
) (*KubernetesTransferExecutor, error) {
	if pods == nil {
		return nil, errors.New("kubernetes Pod executor is required")
	}
	if maximumBytes == 0 {
		maximumBytes = defaultMaxBytes
	}
	if maximumBytes < 256<<10 || maximumBytes > 1<<40 {
		return nil, errors.New(
			"file transfer maximum size must be between 256 KiB and 1 TiB",
		)
	}
	return &KubernetesTransferExecutor{
		pods:         pods,
		maximumBytes: maximumBytes,
	}, nil
}

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
		return Outcome{}, errors.New(
			"uploaded content does not match declared size and checksum",
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
	var stdout bytes.Buffer
	quoted := shellquote.Join(temporary)
	script := "if [ -L " + quoted + " ]; then exit 74; " +
		"elif [ -f " + quoted + " ]; then wc -c < " + quoted + "; " +
		"elif [ -e " + quoted + " ]; then exit 75; else echo 0; fi"
	if err := executor.shell(ctx, identity, namespace, spec, script, nil, &stdout); err != nil {
		return 0, err
	}
	offset, err := strconv.ParseUint(strings.TrimSpace(stdout.String()), 10, 64)
	if err != nil || offset > executor.maximumBytes {
		return 0, errors.New(
			"container returned an invalid partial upload size",
		)
	}
	return offset, nil
}

func uploadTemporaryPath(spec Spec, taskID string) string {
	id := taskID
	if spec.ResumeID != "" {
		id = spec.ResumeID
	}
	return spec.RemotePath + ".kubeloop-" + id + ".part"
}

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
		precondition = "test \"$(wc -c < " + shellquote.Join(
			temporary,
		) + ")\" -eq " + strconv.FormatUint(
			spec.Offset,
			10,
		) + "; "
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

func (executor *KubernetesTransferExecutor) uploadValidatedArchive(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	temporary string,
	input io.Reader,
) error {
	reader, writer := io.Pipe()
	validationResult := make(chan error, 1)
	go func() {
		err := validateArchive(ctx, input, writer, executor.maximumBytes)
		_ = writer.CloseWithError(err)
		validationResult <- err
	}()
	script := "set -eu; mkdir -p -- " + shellquote.Join(
		path.Dir(temporary),
	) + "; cat > " + shellquote.Join(
		temporary,
	)
	execErr := executor.shell(
		ctx,
		identity,
		namespace,
		spec,
		script,
		reader,
		io.Discard,
	)
	if execErr != nil {
		_ = reader.CloseWithError(execErr)
	}
	validationErr := <-validationResult
	if validationErr != nil {
		return validationErr
	}
	return execErr
}

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

func physicalPathGuard(root, target string) string {
	return "set -eu; root=$(CDPATH= cd -P " + shellquote.Join(
		root,
	) + " 2>/dev/null && pwd -P); " +
		"target=$(CDPATH= cd -P " + shellquote.Join(
		target,
	) + " 2>/dev/null && pwd -P); " +
		"if [ \"$root\" != / ]; then case \"$target\" in \"$root\"|\"$root\"/*) ;; " +
		"*) echo 'container path is outside the configured allowed root' >&2; exit 73;; esac; fi; "
}

// PhysicalPathGuard emits a Control Plane-owned shell prefix that rejects a
// target directory whose physical path escapes the configured root.
func PhysicalPathGuard(
	root, target string,
) string {
	return physicalPathGuard(root, target)
}

func validateArchive(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	maximum uint64,
) error {
	archive := tar.NewReader(io.TeeReader(input, output))
	var total uint64
	for entries := 0; ; entries++ {
		if entries >= maximumArchiveEntries {
			return errors.New("archive contains too many entries")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errors.New("read upload archive")
		}
		if err := validateArchiveHeader(header, &total, maximum); err != nil {
			return err
		}
		if _, err := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: archive}); err != nil {
			return err
		}
	}
}

func sanitizeArchive(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
) error {
	archive := tar.NewReader(input)
	clean := tar.NewWriter(output)
	var total uint64
	for entries := 0; ; entries++ {
		if entries >= maximumArchiveEntries {
			return errors.New("archive contains too many entries")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return clean.Close()
		}
		if err != nil {
			return errors.New("read download archive")
		}
		if err := validateArchiveHeader(header, &total, ^uint64(0)); err != nil {
			return err
		}
		sanitized := &tar.Header{
			Name: path.Clean(
				strings.ReplaceAll(header.Name, "\\", "/"),
			), Mode: header.Mode & 0o777,
			Size: header.Size, ModTime: header.ModTime.UTC().Truncate(time.Second), Typeflag: header.Typeflag,
		}
		if err := clean.WriteHeader(sanitized); err != nil {
			return err
		}
		if _, err := io.Copy(clean, &contextReader{ctx: ctx, reader: archive}); err != nil {
			return err
		}
	}
}

func validateArchiveHeader(
	header *tar.Header,
	total *uint64,
	maximum uint64,
) error {
	name := strings.ReplaceAll(header.Name, "\\", "/")
	cleaned := path.Clean(name)
	if name == "" || path.IsAbs(name) || cleaned == ".." ||
		strings.HasPrefix(cleaned, "../") ||
		containsParentPathComponent(name) {
		return errors.New("archive path traversal is not allowed")
	}
	if header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg {
		return errors.New("archive links and special files are not allowed")
	}
	if header.Size < 0 || *total > maximum ||
		uint64(header.Size) > maximum-*total {
		return errors.New("archive contents exceed the configured size limit")
	}
	*total += uint64(header.Size)
	return nil
}

func containsParentPathComponent(value string) bool {
	for component := range strings.SplitSeq(value, "/") {
		if component == ".." {
			return true
		}
	}
	return false
}

type countingWriter struct {
	writer  io.Writer
	maximum uint64
	written uint64
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	if writer.written > writer.maximum ||
		uint64(len(value)) > writer.maximum-writer.written {
		return 0, errors.New("download exceeds the configured size limit")
	}
	n, err := writer.writer.Write(value)
	if n > 0 {
		writer.written += uint64(n)
	}
	return n, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}

type limitedBuffer struct {
	buffer  bytes.Buffer
	maximum int
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.buffer.Write(value)
	}
	return original, nil
}

func (buffer *limitedBuffer) String() string { return buffer.buffer.String() }

var _ TransferExecutor = (*KubernetesTransferExecutor)(nil)
