package fsatomic

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteFile replaces path with data written through a same-directory temporary
// file. The rename keeps readers from observing a partially written file.
func WriteFile(path string, data []byte, dirMode, fileMode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	temp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	if err := temp.Chmod(fileMode); err != nil {
		_ = temp.Close()
		return fmt.Errorf("set temporary file permissions: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("replace file: %w", err)
	}
	return nil
}

// CleanupTemps removes regular temporary files left by interrupted WriteFile
// calls for path. It deliberately leaves directories and symlinks untouched.
func CleanupTemps(path string) error {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*"))
	if err != nil {
		return fmt.Errorf("find temporary files: %w", err)
	}
	var result error
	for _, match := range matches {
		info, err := os.Lstat(match)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				result = errors.Join(result, err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := os.Remove(match); err != nil && !errors.Is(err, os.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}
