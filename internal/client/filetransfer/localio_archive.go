package filetransfer

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"errors"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const maximumLocalArchiveEntries = 100_000

func createArchive(ctx context.Context, root string, destination *os.File, maximum uint64) ([32]byte, uint64, error) {
	rootInfo, err := os.Lstat(root)
	if err != nil || !rootInfo.IsDir() {
		return [32]byte{}, 0, errors.New("local directory upload source is not a directory")
	}
	hash := sha256.New()
	bounded := &boundedWriter{writer: io.MultiWriter(destination, hash), maximum: maximum}
	archive := tar.NewWriter(bounded)
	sourceRoot, err := os.OpenRoot(root)
	if err != nil {
		return [32]byte{}, 0, fmt.Errorf("open local directory upload root: %w", err)
	}
	entries := 0
	walkErr := filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entries++
		if entries > maximumLocalArchiveEntries {
			return errors.New("local directory contains too many entries")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("local directory contains an unsupported path: %s", filename)
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if name == "" {
			name = "."
		}
		header := &tar.Header{
			Name: name, Mode: int64(info.Mode().Perm()), ModTime: info.ModTime().UTC().Truncate(time.Second),
		}
		if info.IsDir() {
			header.Typeflag = tar.TypeDir
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = info.Size()
		}
		if err := archive.WriteHeader(header); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		file, err := sourceRoot.Open(relative)
		if err != nil {
			return err
		}
		openedInfo, statErr := file.Stat()
		if statErr != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) {
			_ = file.Close()
			return errors.New("local directory changed while creating its upload snapshot")
		}
		_, copyErr := io.CopyN(archive, &contextReader{ctx: ctx, reader: file}, info.Size())
		closeErr := file.Close()
		return errors.Join(copyErr, closeErr)
	})
	rootCloseErr := sourceRoot.Close()
	closeErr := archive.Close()
	if walkErr != nil || rootCloseErr != nil || closeErr != nil {
		return [32]byte{}, 0, errors.Join(walkErr, rootCloseErr, closeErr)
	}
	if bounded.written == 0 {
		return [32]byte{}, 0, errors.New("local directory archive is empty")
	}
	if err := destination.Sync(); err != nil {
		return [32]byte{}, 0, err
	}
	var checksum [32]byte
	copy(checksum[:], hash.Sum(nil))
	return checksum, bounded.written, nil
}

func extractArchive(ctx context.Context, input io.Reader, root string, maximum uint64) error {
	archive := tar.NewReader(input)
	var total uint64
	type directoryPermission struct {
		path string
		mode os.FileMode
	}
	directories := make([]directoryPermission, 0)
	seen := make(map[string]struct{})
	for entries := 0; ; entries++ {
		if entries >= maximumLocalArchiveEntries {
			return errors.New("downloaded directory contains too many entries")
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			for _, v := range slices.Backward(directories) {
				if err := os.Chmod(v.path, v.mode); err != nil {
					return err
				}
			}
			return nil
		}
		if err != nil {
			return errors.New("downloaded directory archive is invalid")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		name := strings.ReplaceAll(header.Name, "\\", "/")
		cleaned := path.Clean(name)
		if name == "" || path.IsAbs(name) || cleaned == ".." || strings.HasPrefix(cleaned, "../") ||
			utils.ContainsParentPathComponent(name) ||
			(header.Typeflag != tar.TypeDir && header.Typeflag != tar.TypeReg) ||
			header.Size < 0 || total > maximum || uint64(header.Size) > maximum-total {
			return errors.New("downloaded directory archive contains an unsafe entry")
		}
		total += uint64(header.Size)
		if _, exists := seen[cleaned]; exists {
			return errors.New("downloaded directory archive contains a duplicate entry")
		}
		seen[cleaned] = struct{}{}
		mode := os.FileMode(header.Mode & 0o777)
		if cleaned == "." {
			if header.Typeflag != tar.TypeDir {
				return errors.New("downloaded directory root entry is invalid")
			}
			directories = append(directories, directoryPermission{path: root, mode: mode})
			continue
		}
		target := filepath.Join(root, filepath.FromSlash(cleaned))
		relative, err := filepath.Rel(root, target)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return errors.New("downloaded directory path escapes its destination")
		}
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
			directories = append(directories, directoryPermission{path: target, mode: mode})
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o750); err != nil {
			return err
		}
		file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(file, &contextReader{ctx: ctx, reader: archive}, header.Size)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return errors.Join(copyErr, closeErr)
		}
	}
}
