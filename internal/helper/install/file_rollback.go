package install

import (
	"fmt"
	"os"
)

type fileRollback struct {
	path       string
	backupPath string
	existed    bool
	mode       os.FileMode
}

func snapshotFileForRollback(path string) (fileRollback, error) {
	snapshot := fileRollback{
		path:       path,
		backupPath: path + ".kubeloop-rollback",
	}
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			_ = os.Remove(snapshot.backupPath)
			return snapshot, nil
		}
		return snapshot, err
	}
	if info.IsDir() {
		return snapshot, fmt.Errorf("%s is a directory", path)
	}
	snapshot.existed = true
	snapshot.mode = info.Mode().Perm()
	if err := copyFile(path, snapshot.backupPath, snapshot.mode); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (f fileRollback) restore() error {
	if !f.existed {
		if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := replaceFile(f.backupPath, f.path); err != nil {
		return err
	}
	return os.Chmod(f.path, f.mode)
}

func (f fileRollback) discard() {
	_ = os.Remove(f.backupPath)
	_ = os.Remove(f.backupPath + ".tmp")
}
