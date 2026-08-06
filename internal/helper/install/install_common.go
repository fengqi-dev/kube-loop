package install

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/helper"
)

func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
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

func InstallFromCLI(source, token string, uid int, version, homeDir, ownerSID, singBoxPath string) error {
	if source == "" {
		return fmt.Errorf("--source is required")
	}
	if token == "" {
		return fmt.Errorf("--token is required")
	}
	if version == "" {
		version = helper.Version
	}
	if homeDir == "" {
		return fmt.Errorf("--home is required")
	}
	if singBoxPath == "" {
		located, locateErr := helper.LocateBundledSingBox()
		if locateErr == nil {
			singBoxPath = located
		} else if near := singBoxNearHelperSource(source); near != "" {
			singBoxPath = near
		} else {
			return locateErr
		}
	}
	if info, err := os.Stat(singBoxPath); err != nil || info.IsDir() {
		return fmt.Errorf("sing-box binary not found at %s", singBoxPath)
	}
	if abs, err := filepath.Abs(singBoxPath); err == nil {
		singBoxPath = abs
	}
	dest := helper.BinaryInstallPath()
	needsBinaryUpdate, err := helperNeedsBinaryUpdate(source, dest)
	if err != nil {
		return err
	}
	if needsBinaryUpdate {
		if err := prepareBinaryInstall(); err != nil {
			return fmt.Errorf("prepare helper binary install: %w", err)
		}
		if !sameInstallPath(source, dest) {
			if err := copyFile(source, dest, 0o755); err != nil {
				return fmt.Errorf("install helper binary: %w", err)
			}
		}
	}
	if err := helper.WriteSystemAuth(helper.AuthFile{
		Token:       token,
		UID:         uid,
		Version:     version,
		HomeDir:     homeDir,
		OwnerSID:    ownerSID,
		SingBoxPath: singBoxPath,
	}); err != nil {
		return err
	}
	if err := enableService(dest); err != nil {
		return err
	}
	cleanupDisplacedHelperBinaries(dest)
	return nil
}

func singBoxNearHelperSource(source string) string {
	name := "sing-box"
	if runtime.GOOS == "windows" {
		name = "sing-box.exe"
	}
	dir := filepath.Dir(source)
	for _, candidate := range []string{
		filepath.Join(dir, name),
		filepath.Join(dir, "..", name),
		filepath.Join(filepath.Dir(dir), name),
	} {
		info, err := os.Stat(candidate)
		if err == nil && !info.IsDir() {
			abs, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return filepath.Clean(candidate)
			}
			return abs
		}
	}
	return ""
}

func helperNeedsBinaryUpdate(source, dest string) (bool, error) {
	if sameInstallPath(source, dest) {
		return false, nil
	}
	info, err := os.Stat(dest)
	if err != nil {
		if os.IsNotExist(err) {
			return true, nil
		}
		return false, fmt.Errorf("stat installed helper: %w", err)
	}
	if info.IsDir() {
		return true, nil
	}
	srcHash, err := fileSHA256(source)
	if err != nil {
		return false, fmt.Errorf("hash helper source: %w", err)
	}
	dstHash, err := fileSHA256(dest)
	if err != nil {
		return true, nil
	}
	return !strings.EqualFold(srcHash, dstHash), nil
}

func UninstallFromCLI() error {
	if err := disableService(); err != nil {
		return err
	}
	current := helper.BinaryInstallPath()
	_ = os.Remove(current)
	if legacy := helper.LegacyBinaryInstallPath(); legacy != "" {
		_ = os.Remove(legacy)
	}
	cleanupDisplacedHelperBinaries(current)
	_ = os.Remove(helper.SystemAuthPath())
	_ = os.Remove(helper.SystemTokenPath())
	_ = os.Remove(helper.SocketPath())
	return nil
}

func sameInstallPath(a, b string) bool {
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	aAbs, bAbs = filepath.Clean(aAbs), filepath.Clean(bAbs)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(aAbs, bAbs)
	}
	return aAbs == bAbs
}
