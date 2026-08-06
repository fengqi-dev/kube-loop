package filemanager

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

func localStat(rawPath string) (FileEntry, error) {
	cleaned, err := filepath.Abs(filepath.Clean(rawPath))
	if err != nil {
		return FileEntry{}, err
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return FileEntry{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return FileEntry{}, errors.New("symbolic links are not supported")
	}
	return FileEntry{
		Name: info.Name(), Path: cleaned, Dir: info.IsDir(), Size: info.Size(),
		Mode: uint32(info.Mode().Perm()), ModTime: info.ModTime(),
	}, nil
}

func localTreeSize(root string) (int64, error) {
	var total int64
	err := filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not supported", info.Name())
		}
		if info.Mode().IsRegular() {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func writeArchive(
	ctx context.Context,
	root string,
	output io.Writer,
	update func(int64),
) error {
	archive := tar.NewWriter(output)
	var done int64
	err := filepath.Walk(root, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symbolic link %q is not supported", relative)
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("special file %q is not supported", relative)
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if info.IsDir() {
			header.Name += "/"
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		reader := &progressReader{reader: file, done: done, update: update}
		_, copyErr := io.Copy(archive, reader)
		closeErr := file.Close()
		done = reader.done
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if err != nil {
		_ = archive.Close()
		return err
	}
	return archive.Close()
}

func extractArchive(
	ctx context.Context,
	input io.Reader,
	root string,
	update func(int64),
) error {
	archive := tar.NewReader(input)
	var done int64
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		target, err := safeArchiveTarget(root, header.Name)
		if err != nil {
			return err
		}
		if target == root {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(header.Mode)&0o777); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(
				target,
				os.O_CREATE|os.O_WRONLY|os.O_TRUNC,
				os.FileMode(header.Mode)&0o777,
			)
			if err != nil {
				return err
			}
			writer := &progressWriter{writer: file, done: done, update: update}
			_, copyErr := io.Copy(writer, archive)
			closeErr := file.Close()
			done = writer.done
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive entry %q has unsupported type", header.Name)
		}
	}
}

func safeArchiveTarget(root, name string) (string, error) {
	cleaned := path.Clean(strings.ReplaceAll(name, "\\", "/"))
	if cleaned == "." {
		return root, nil
	}
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	target := filepath.Join(root, filepath.FromSlash(cleaned))
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry %q escapes the destination", name)
	}
	return target, nil
}

func sortEntries(items []FileEntry) {
	sort.Slice(items, func(i, j int) bool {
		if items[i].Dir != items[j].Dir {
			return items[i].Dir
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})
}

func validateTarget(target Target) error {
	if target.Context == "" || target.Namespace == "" || target.Pod == "" || target.Container == "" {
		return errors.New("context, namespace, pod, and container are required")
	}
	return nil
}

func validateEntryName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." ||
		strings.ContainsAny(name, `/\`) {
		return "", errors.New("name must be a single non-empty path component")
	}
	return name, nil
}

func cleanLocalPath(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("local path is required")
	}
	return filepath.Abs(filepath.Clean(raw))
}

func isLocalRoot(cleaned string) bool {
	return filepath.Dir(cleaned) == cleaned
}

func (m *Manager) validatePodIdentity(ctx context.Context, target Target) error {
	if target.PodUID == "" || m.catalog == nil {
		return nil
	}
	pods, err := m.catalog.ListPods(ctx, target.Context, target.Namespace)
	if err != nil {
		return fmt.Errorf("verify Pod identity: %w", err)
	}
	for _, pod := range pods {
		if pod.Namespace == target.Namespace && pod.Name == target.Pod {
			if pod.UID != target.PodUID {
				return errors.New("Pod was replaced")
			}
			if !pod.Ready {
				return errors.New("Pod is not ready")
			}
			if slices.Contains(pod.Containers, target.Container) {
				return nil
			}
			return errors.New("container no longer exists")
		}
	}
	return errors.New("Pod no longer exists")
}

func cleanRemotePath(raw string) (string, error) {
	cleaned := path.Clean(strings.TrimSpace(raw))
	if !path.IsAbs(cleaned) {
		return "", errors.New("container path must be absolute")
	}
	return cleaned, nil
}

func podTarget(target Target) podssh.Target {
	return podssh.Target{
		Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container,
	}
}

func sameModTime(left, right time.Time) bool {
	return left.Unix() == right.Unix()
}
