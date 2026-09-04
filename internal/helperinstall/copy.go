package helperinstall

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func copyFile(src, dst string, mode os.FileMode) error {
	//nolint:gosec // System executable directories must be traversable by service launchers.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	copiedHash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, copiedHash), in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	staged, err := os.Open(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	stagedHash := sha256.New()
	_, hashErr := io.Copy(stagedHash, staged)
	closeErr := staged.Close()
	if hashErr != nil {
		_ = os.Remove(tmp)
		return hashErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if !bytes.Equal(copiedHash.Sum(nil), stagedHash.Sum(nil)) {
		_ = os.Remove(tmp)
		return fmt.Errorf("staged helper hash does not match source")
	}
	if err := replaceFile(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Chmod(dst, mode)
}
