package fileopsapi

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kballard/go-shellquote"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
)

const (
	defaultMaximumOutput  = 8 << 20
	defaultMaximumEntries = 10_000
)

type Operator interface {
	List(
		context.Context,
		controlplaneapi.Identity,
		string,
		Spec,
	) ([]Entry, error)
	Mutate(context.Context, controlplaneapi.Identity, string, Spec) error
}

type KubernetesOperator struct {
	pods           fileapi.PodExecutor
	maximumOutput  int
	maximumEntries int
}

func NewKubernetesOperator(
	pods fileapi.PodExecutor,
) (*KubernetesOperator, error) {
	if pods == nil {
		return nil, errors.New("pod executor is required")
	}
	return &KubernetesOperator{
		pods:           pods,
		maximumOutput:  defaultMaximumOutput,
		maximumEntries: defaultMaximumEntries,
	}, nil
}

func (operator *KubernetesOperator) List(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
) ([]Entry, error) {
	inner := `for item do mode=$(stat -c %f -- "$item") || exit; ` +
		`size=$(stat -c %s -- "$item") || exit; ` +
		`modified=$(stat -c %Y -- "$item") || exit; ` +
		`printf '%s\t%s\t%s\t%s\000' "$mode" "$size" "$modified" "$item"; done`
	script := fileapi.PhysicalPathGuard(spec.AllowedRoot, spec.Path) +
		"test ! -L " + shellquote.Join(spec.Path) + "; test -d " + shellquote.Join(spec.Path) + "; " +
		"find " + shellquote.Join(spec.Path) + " -mindepth 1 -maxdepth 1 -exec sh -c " + shellquote.Join(inner) + " sh {} +"
	output := &boundedBuffer{maximum: operator.maximumOutput}
	if err := operator.shell(ctx, identity, namespace, spec, script, output); err != nil {
		return nil, err
	}
	entries, err := parseEntries(output.Bytes(), operator.maximumEntries)
	if err != nil {
		return nil, err
	}
	slices.SortFunc(entries, func(left, right Entry) int {
		leftDirectory := left.Kind == KindDirectory
		rightDirectory := right.Kind == KindDirectory
		if leftDirectory != rightDirectory {
			if leftDirectory {
				return -1
			}
			return 1
		}
		return strings.Compare(
			strings.ToLower(left.Name),
			strings.ToLower(right.Name),
		)
	})
	return entries, nil
}

func (operator *KubernetesOperator) Mutate(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
) error {
	var script string
	switch spec.Action {
	case ActionCreate:
		guard := fileapi.PhysicalPathGuard(
			spec.AllowedRoot,
			path.Dir(spec.Path),
		)
		quoted := shellquote.Join(spec.Path)
		if spec.Kind == KindDirectory {
			script = guard + "if [ -e " + quoted + " ] || [ -L " + quoted + "]; then " +
				"test ! -L " + quoted + " && test -d " + quoted + "; " +
				"else mkdir -- " + quoted + "; fi"
		} else {
			script = guard + "if [ -e " + quoted + " ] || [ -L " + quoted + "]; then " +
				"test ! -L " + quoted + " && test -f " + quoted + "; " +
				"else : > " + quoted + "; fi"
		}
	case ActionRename:
		guard := fileapi.PhysicalPathGuard(
			spec.AllowedRoot,
			path.Dir(spec.Path),
		) +
			fileapi.PhysicalPathGuard(
				spec.DestinationRoot,
				path.Dir(spec.Destination),
			)
		source, destination := shellquote.Join(
			spec.Path,
		), shellquote.Join(
			spec.Destination,
		)
		script = guard + "if [ -e " + source + " ] || [ -L " + source + " ]; then " +
			"test ! -L " + source + "; test ! -e " + destination +
			" && test ! -L " + destination + "; mv -- " + source + " " + destination +
			"; else test -e " + destination + " && test ! -L " + destination + "; fi"
	case ActionDelete:
		guard := fileapi.PhysicalPathGuard(
			spec.AllowedRoot,
			path.Dir(spec.Path),
		)
		quoted := shellquote.Join(spec.Path)
		remove := "if [ -d " + quoted + " ]; then rmdir -- " + quoted + "; else rm -- " + quoted + "; fi"
		if spec.Recursive {
			remove = "rm -rf -- " + quoted
		}
		script = guard + "if [ -e " + quoted + " ] || [ -L " + quoted + "]; then " +
			"test ! -L " + quoted + "; " + remove + "; fi"
	default:
		return errors.New("unsupported remote file action")
	}
	return operator.shell(ctx, identity, namespace, spec, script, io.Discard)
}

func (operator *KubernetesOperator) shell(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
	spec Spec,
	script string,
	stdout io.Writer,
) error {
	stderr := &boundedBuffer{maximum: 4096}
	err := operator.pods.Exec(ctx, identity, namespace, execapi.Spec{
		Pod: spec.Pod, Container: spec.Container, Command: []string{"/bin/sh", "-c", "set -eu; " + script},
	}, execapi.Streams{Stdout: stdout, Stderr: stderr})
	if err != nil {
		return fmt.Errorf("remote file operation failed: %w", err)
	}
	return nil
}

type boundedBuffer struct {
	bytes.Buffer

	maximum int
}

func (buffer *boundedBuffer) Write(value []byte) (int, error) {
	if buffer.Len()+len(value) > buffer.maximum {
		return 0, errors.New("remote file operation output exceeded its limit")
	}
	return buffer.Buffer.Write(value)
}

func parseEntries(raw []byte, maximum int) ([]Entry, error) {
	records := bytes.Split(raw, []byte{0})
	if len(records) > 0 && len(records[len(records)-1]) == 0 {
		records = records[:len(records)-1]
	}
	if len(records) > maximum {
		return nil, errors.New("remote directory contains too many entries")
	}
	entries := make([]Entry, 0, len(records))
	for _, record := range records {
		fields := bytes.SplitN(record, []byte{'\t'}, 4)
		if len(fields) != 4 {
			return nil, errors.New("remote directory entry is malformed")
		}
		mode, err := strconv.ParseUint(string(fields[0]), 16, 32)
		if err != nil {
			return nil, errors.New("remote directory entry mode is invalid")
		}
		size, err := strconv.ParseInt(string(fields[1]), 10, 64)
		if err != nil || size < 0 {
			return nil, errors.New("remote directory entry size is invalid")
		}
		seconds, err := strconv.ParseInt(string(fields[2]), 10, 64)
		if err != nil {
			return nil, errors.New(
				"remote directory entry timestamp is invalid",
			)
		}
		entryPath := string(fields[3])
		if entryPath == "" || strings.IndexByte(entryPath, 0) >= 0 {
			return nil, errors.New("remote directory entry path is invalid")
		}
		entries = append(entries, Entry{
			Name: path.Base(
				entryPath,
			), Path: entryPath, Kind: kindFromMode(mode), Size: size,
			Mode: fmt.Sprintf(
				"%04o",
				mode&0o7777,
			), ModifiedAt: time.Unix(seconds, 0).UTC(),
		})
	}
	return entries, nil
}

func kindFromMode(mode uint64) string {
	switch mode & 0o170000 {
	case 0o040000:
		return KindDirectory
	case 0o100000:
		return KindFile
	case 0o120000:
		return KindSymlink
	default:
		return KindOther
	}
}

var _ Operator = (*KubernetesOperator)(nil)
