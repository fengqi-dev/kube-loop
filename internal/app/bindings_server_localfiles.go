package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type ServerLocalFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Kind       string    `json:"kind"`
	Size       int64     `json:"size"`
	Mode       uint32    `json:"mode"`
	ModifiedAt time.Time `json:"modifiedAt" ts_type:"string"`
}

func (a *App) ServerLocalHomeDirectory() (string, error) {
	return os.UserHomeDir()
}

func (a *App) ListServerLocalFiles(value string) ([]ServerLocalFileEntry, error) {
	directory, err := cleanServerLocalPath(value)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, errors.New("read local directory")
	}
	result := make([]ServerLocalFileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		kind := serverFileKindFile
		switch {
		case entry.IsDir():
			kind = serverFileKindDirectory
		case info.Mode()&os.ModeSymlink != 0:
			kind = "symlink"
		case !info.Mode().IsRegular():
			kind = "other"
		}
		result = append(result, ServerLocalFileEntry{
			Name: entry.Name(), Path: filepath.Join(directory, entry.Name()), Kind: kind,
			Size: info.Size(), Mode: uint32(info.Mode().Perm()), ModifiedAt: info.ModTime().UTC(),
		})
	}
	slices.SortFunc(result, func(left, right ServerLocalFileEntry) int {
		if (left.Kind == serverFileKindDirectory) != (right.Kind == serverFileKindDirectory) {
			if left.Kind == serverFileKindDirectory {
				return -1
			}
			return 1
		}
		return strings.Compare(strings.ToLower(left.Name), strings.ToLower(right.Name))
	})
	return result, nil
}

func (a *App) CreateServerLocalFile(parent, name, kind string) error {
	destination, err := serverLocalChild(parent, name)
	if err != nil {
		return err
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case serverFileKindFile:
		file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return errors.New("create local file")
		}
		return file.Close()
	case serverFileKindDirectory:
		if err := os.Mkdir(destination, 0o700); err != nil {
			return errors.New("create local directory")
		}
		return nil
	default:
		return errors.New("local entry kind must be file or directory")
	}
}

func (a *App) RenameServerLocalFile(value, name string) error {
	source, err := cleanServerLocalPath(value)
	if err != nil {
		return err
	}
	destination, err := serverLocalChild(filepath.Dir(source), name)
	if err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return errors.New("rename local entry")
	}
	return nil
}

func (a *App) DeleteServerLocalFile(value string) error {
	target, err := cleanServerLocalPath(value)
	if err != nil {
		return err
	}
	if filepath.Dir(target) == target {
		return errors.New("refusing to delete a filesystem root")
	}
	if err := os.RemoveAll(target); err != nil {
		return errors.New("delete local entry")
	}
	return nil
}

func cleanServerLocalPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return "", errors.New("local path is required")
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", errors.New("resolve local path")
	}
	return filepath.Clean(absolute), nil
}

func serverLocalChild(parent, name string) (string, error) {
	parent, err := cleanServerLocalPath(parent)
	if err != nil {
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." || filepath.Base(name) != name || strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("local entry name is invalid")
	}
	return filepath.Join(parent, name), nil
}
