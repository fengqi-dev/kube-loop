package fileapi

import (
	"archive/tar"
	"context"
	"errors"
	"io"
	"path"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

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
