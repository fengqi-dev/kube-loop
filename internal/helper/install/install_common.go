package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

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

func InstallFromCLI(source, token string, uid int, version, homeDir, ownerSID, singBoxPath string) (retErr error) {
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
	rollback, err := beginInstallRollback(
		dest,
		helper.SystemAuthPath(),
		helper.SystemTokenPath(),
	)
	if err != nil {
		return fmt.Errorf("prepare helper rollback: %w", err)
	}
	defer func() {
		if retErr == nil {
			rollback.commit()
			return
		}
		if rollbackErr := rollback.restore(); rollbackErr != nil {
			retErr = errors.Join(retErr, fmt.Errorf("rollback helper install: %w", rollbackErr))
		}
	}()
	if err := prepareBinaryInstall(); err != nil {
		return fmt.Errorf("stop helper service for install: %w", err)
	}
	if needsBinaryUpdate && !sameInstallPath(source, dest) {
		if err := copyFile(source, dest, 0o755); err != nil {
			return fmt.Errorf("install helper binary: %w", err)
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
	if err := waitForInstalledHelperReady(token, version); err != nil {
		return err
	}
	cleanupDisplacedHelperBinaries(dest)
	return nil
}

const installReadyTimeout = 20 * time.Second

func waitForInstalledHelperReady(token, version string) error {
	ctx, cancel := context.WithTimeout(context.Background(), installReadyTimeout)
	defer cancel()
	client := &helper.Client{Token: token}
	return waitForHelperReady(ctx, installReadyTimeout, 100*time.Millisecond, func(pingCtx context.Context) (helper.Response, error) {
		requestCtx, requestCancel := context.WithTimeout(pingCtx, 2*time.Second)
		defer requestCancel()
		response, err := client.Ping(requestCtx)
		if err == nil && response.Version != version {
			return response, fmt.Errorf(
				"helper version %q does not match installed version %q",
				response.Version,
				version,
			)
		}
		return response, err
	})
}

type installRollback struct {
	binary fileRollback
	auth   fileRollback
	token  fileRollback
}

type fileRollback struct {
	path       string
	backupPath string
	existed    bool
	mode       os.FileMode
}

func beginInstallRollback(binaryPath, authPath, tokenPath string) (*installRollback, error) {
	binary, err := snapshotFileForRollback(binaryPath)
	if err != nil {
		return nil, err
	}
	auth, err := snapshotFileForRollback(authPath)
	if err != nil {
		binary.discard()
		return nil, err
	}
	token, err := snapshotFileForRollback(tokenPath)
	if err != nil {
		binary.discard()
		auth.discard()
		return nil, err
	}
	return &installRollback{binary: binary, auth: auth, token: token}, nil
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

func (r *installRollback) commit() {
	r.binary.discard()
	r.auth.discard()
	r.token.discard()
}

func (r *installRollback) restore() error {
	var rollbackErrs []error
	if err := prepareBinaryInstall(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("stop failed service: %w", err))
	}
	if err := r.binary.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper binary: %w", err))
	}
	if err := r.auth.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper auth: %w", err))
	}
	if err := r.token.restore(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("restore helper token: %w", err))
	}
	if r.binary.existed {
		if err := enableService(r.binary.path); err != nil {
			rollbackErrs = append(rollbackErrs, fmt.Errorf("restart previous helper: %w", err))
		}
	} else if err := disableService(); err != nil {
		rollbackErrs = append(rollbackErrs, fmt.Errorf("remove failed helper service: %w", err))
	}
	rollbackErr := errors.Join(rollbackErrs...)
	if rollbackErr == nil {
		r.commit()
	}
	return rollbackErr
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
