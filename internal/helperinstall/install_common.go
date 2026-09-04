package helperinstall

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const goosWindows = "windows"

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
	coreDest := helper.CoreInstallPath()
	needsBinaryUpdate, err := helperNeedsBinaryUpdate(source, dest)
	if err != nil {
		return err
	}
	needsCoreUpdate, err := helperNeedsBinaryUpdate(singBoxPath, coreDest)
	if err != nil {
		return err
	}
	rollback, err := beginInstallRollback(
		dest,
		coreDest,
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
	if needsCoreUpdate && !sameInstallPath(singBoxPath, coreDest) {
		if err := copyFile(singBoxPath, coreDest, 0o755); err != nil {
			return fmt.Errorf("install sing-box core: %w", err)
		}
	}
	if err := helper.WriteSystemAuth(helper.AuthFile{
		Token:       token,
		UID:         uid,
		Version:     version,
		HomeDir:     homeDir,
		OwnerSID:    ownerSID,
		SingBoxPath: coreDest,
	}); err != nil {
		return err
	}
	if err := enableService(dest); err != nil {
		return err
	}
	if err := waitForInstalledHelperReady(token, version); err != nil {
		return err
	}
	return nil
}

// launchd can throttle a replaced service for longer than its ordinary startup
// time. Keep the first-install wait bounded but allow the service to outlive a
// transient launchd delay instead of reporting a false installation failure.
const installReadyTimeout = 90 * time.Second

func waitForInstalledHelperReady(token, version string) error {
	client := &helper.Client{Token: token}
	return waitForHelperReady(
		context.Background(),
		installReadyTimeout,
		100*time.Millisecond,
		func(pingCtx context.Context) (helperrpc.Response, error) {
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
		},
	)
}

func singBoxNearHelperSource(source string) string {
	name := "sing-box"
	if runtime.GOOS == goosWindows {
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
	srcHash, err := utils.FileSHA256(source)
	if err != nil {
		return false, fmt.Errorf("hash helper source: %w", err)
	}
	dstHash, err := utils.FileSHA256(dest)
	if err != nil {
		//nolint:nilerr // The caller needs the reinstall decision, not the stale-file read error.
		return true, nil
	}
	return !strings.EqualFold(srcHash, dstHash), nil
}

func UninstallFromCLI() error {
	if err := disableService(); err != nil {
		return err
	}
	current := helper.BinaryInstallPath()
	removeInstalledBinary(current)
	_ = os.Remove(helper.SystemAuthPath())
	_ = os.Remove(helper.SystemTokenPath())
	_ = os.Remove(helper.SocketPath())
	return nil
}

func removeInstalledBinary(path string) {
	// A unified Windows helper performs uninstall from the same executable used
	// by the service. Windows cannot unlink that executable while this process is
	// running; keep the packaged resource so the desktop can install it again.
	// The application uninstaller removes the containing directory after this
	// command exits.
	if runtime.GOOS == goosWindows {
		if executable, err := os.Executable(); err == nil && sameInstallPath(path, executable) {
			return
		}
	}
	_ = os.Remove(path)
}

func sameInstallPath(a, b string) bool {
	aAbs, errA := filepath.Abs(a)
	bAbs, errB := filepath.Abs(b)
	if errA != nil || errB != nil {
		return false
	}
	aAbs, bAbs = filepath.Clean(aAbs), filepath.Clean(bAbs)
	if runtime.GOOS == goosWindows {
		return strings.EqualFold(aAbs, bAbs)
	}
	return aAbs == bAbs
}
