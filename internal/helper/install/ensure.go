package install

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
)

const (
	helperServiceName     = "kubeloop-helper"
	supervisorServiceName = "kubeloop-supervisor"
)

var (
	bundledFilesMu       sync.RWMutex
	bundledFiles         = map[string][]byte{}
	bundledHashes        = map[string]string{}
	materializeBundledMu sync.Mutex
	ensureInstallMu      sync.Mutex
)

// SetBundledBinary supplies the platform helper service embedded by the desktop
// application. The standalone helper binary never calls it.
func SetBundledBinary(content []byte) {
	SetBundledFile(helperBinaryName(helperServiceName), content)
}

// SetBundledFile supplies a named helper-related binary embedded by the desktop app.
func SetBundledFile(name string, content []byte) {
	bundledFilesMu.Lock()
	defer bundledFilesMu.Unlock()
	if len(content) == 0 {
		delete(bundledFiles, name)
		delete(bundledHashes, name)
		return
	}
	bundledFiles[name] = bytes.Clone(content)
	sum := sha256.Sum256(content)
	bundledHashes[name] = hex.EncodeToString(sum[:])
}

// EnsureInstall makes the helper available for automatic TUN startup. Release
// builds always require the installed binary to match the bundled helper. Dev
// builds reuse a healthy, protocol-compatible helper so wails dev does not
// request administrator authorization after every helper rebuild.
func EnsureInstall(ctx context.Context) error {
	return ensureInstall(ctx, false, nil)
}

// EnsureCurrentInstall installs or upgrades to the exact bundled helper. Use it
// for explicit user-driven installs and E2E setup where binary drift must not be
// accepted, including in development builds.
func EnsureCurrentInstall(ctx context.Context) error {
	return ensureInstall(ctx, true, nil)
}

// EnsureCurrentInstallWithCertificate installs the exact bundled helper and,
// when the platform needs a privileged install, trusts certificatePEM in the
// same administrator authorization on macOS, Linux, and Windows. Other update
// paths leave certificate installation to the caller.
func EnsureCurrentInstallWithCertificate(ctx context.Context, certificatePEM []byte) error {
	return ensureInstall(ctx, true, certificatePEM)
}

func ensureInstall(ctx context.Context, requireCurrentBinary bool, certificatePEM []byte) error {
	ensureInstallMu.Lock()
	defer ensureInstallMu.Unlock()

	status := helper.GetStatus(ctx)
	enforceBinaryMatch := mustMatchBundledHelper(requireCurrentBinary, helper.IsDevBuild())
	if !enforceBinaryMatch && canReuseInstalledHelper(
		status, helper.Version, helperprotocol.Version, false, false,
	) {
		return nil
	}
	source, locateErr := LocateBundledHelper()
	needsBinaryUpdate := false
	if locateErr == nil {
		var hashErr error
		needsBinaryUpdate, hashErr = helperNeedsBinaryUpdate(source, helper.BinaryInstallPath())
		if hashErr != nil {
			return hashErr
		}
	}
	if canReuseInstalledHelper(
		status, helper.Version, helperprotocol.Version, enforceBinaryMatch, needsBinaryUpdate,
	) && !requiresSupervisorCheck(enforceBinaryMatch) {
		return nil
	}
	if locateErr != nil {
		return locateErr
	}
	sourceSHA256, err := bundledHelperSHA256(source)
	if err != nil {
		return err
	}
	singBoxPath, err := helper.LocateBundledSingBox()
	if err != nil {
		return err
	}
	token, err := helper.EnsureUserToken()
	if err != nil {
		return err
	}
	home, err := helper.UserHomeDir()
	if err != nil {
		return err
	}
	if err := installCurrentHelper(
		ctx, source, sourceSHA256, token, currentUID(), home, singBoxPath, certificatePEM,
	); err != nil {
		return err
	}
	client := &helper.Client{Token: token}
	return waitForHelperReady(ctx, 20*time.Second, 100*time.Millisecond, func(pingCtx context.Context) (helperprotocol.Response, error) {
		requestCtx, cancel := context.WithTimeout(pingCtx, 2*time.Second)
		defer cancel()
		response, pingErr := client.Ping(requestCtx)
		if pingErr == nil && response.Version != helper.Version {
			return response, fmt.Errorf(
				"helper version %q does not match expected version %q",
				response.Version,
				helper.Version,
			)
		}
		return response, pingErr
	})
}

func mustMatchBundledHelper(requireCurrentBinary, developmentBuild bool) bool {
	return requireCurrentBinary || !developmentBuild
}

func canReuseInstalledHelper(
	status helper.Status,
	expectedVersion string,
	expectedProtocol int,
	enforceBinaryMatch bool,
	needsBinaryUpdate bool,
) bool {
	if !status.Running || !status.CoreReady || status.Version != expectedVersion ||
		status.Protocol != expectedProtocol {
		return false
	}
	return !enforceBinaryMatch || !needsBinaryUpdate
}

func waitForHelperReady(
	ctx context.Context,
	timeout time.Duration,
	interval time.Duration,
	ping func(context.Context) (helperprotocol.Response, error),
) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		response, err := ping(waitCtx)
		if err == nil && response.Protocol == helperprotocol.Version && response.CoreReady {
			return nil
		}
		switch {
		case err != nil:
			lastErr = err
		case response.Protocol != helperprotocol.Version:
			lastErr = fmt.Errorf(
				"helper protocol %d does not match expected protocol %d",
				response.Protocol,
				helperprotocol.Version,
			)
		default:
			lastErr = fmt.Errorf("helper is running but bundled sing-box is not configured")
		}

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-waitCtx.Done():
			timer.Stop()
			if err := ctx.Err(); err != nil {
				return err
			}
			if lastErr != nil {
				return fmt.Errorf("helper did not become ready after install: %w", lastErr)
			}
			return fmt.Errorf("helper did not become ready after install")
		case <-timer.C:
		}
	}
}

// Uninstall removes the helper service (requires elevation).
func Uninstall(ctx context.Context) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	return ElevateUninstall(ctx, source)
}

// UninstallWithCertificate removes the macOS helper and an optional system
// trust certificate in one elevated operation. Other platforms use Uninstall.
func UninstallWithCertificate(ctx context.Context, fingerprint string) error {
	source, err := LocateBundledHelper()
	if err != nil {
		return err
	}
	return ElevateUninstallWithCertificate(ctx, source, fingerprint)
}

func LocateBundledHelper() (string, error) {
	return locateBundledTool(helperServiceName)
}

func LocateBundledSupervisor() (string, error) {
	return locateBundledTool(supervisorServiceName)
}

func locateBundledTool(baseName string) (string, error) {
	name := helperBinaryName(baseName)
	// Unix helpers are installed outside the application bundle. Always
	// materialize the exact bytes embedded in this desktop build first so stale
	// development/package artifacts cannot be selected as the privileged source.
	if runtime.GOOS != "windows" {
		if path, ok, err := materializeBundledFile(name); ok || err != nil {
			return path, err
		}
	}
	// Prefer on-disk package resources ({installRoot}\resources) over
	// materializing the embedded copy — avoids multi-MB read/write on every elevate.
	if path, err := findBundledToolOnDisk(name); err == nil {
		return path, nil
	}
	if path, ok, err := materializeBundledFile(name); ok || err != nil {
		return path, err
	}
	if path, lookErr := exec.LookPath(name); lookErr == nil {
		return path, nil
	}
	exe, _ := os.Executable()
	return "", fmt.Errorf("%s binary not found near %s", name, exe)
}

func findBundledToolOnDisk(name string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return "", err
	}
	cwd, _ := os.Getwd()
	candidates := bundledToolCandidates(exe, cwd, name)
	for _, candidate := range candidates {
		info, statErr := os.Stat(candidate)
		if statErr == nil && !info.IsDir() {
			absolute, absErr := filepath.Abs(candidate)
			if absErr != nil {
				return "", fmt.Errorf("resolve bundled %s path: %w", name, absErr)
			}
			return filepath.Clean(absolute), nil
		}
	}
	return "", fmt.Errorf("%s not found on disk", name)
}

func bundledToolCandidates(exe, cwd, name string) []string {
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "resources", name),
		filepath.Join(dir, "Resources", name),
		filepath.Join(dir, "..", "Resources", name),
		filepath.Join(dir, name),
		filepath.Join(dir, "Helpers", name),
		filepath.Join(dir, "..", "Helpers", name),
		filepath.Join("build", "bin", "resources", name),
		filepath.Join("build", "bin", "Resources", name),
		filepath.Join("build", "bin", name),
	}
	// On Windows the packaged helper is also the service install target under
	// the application's resources directory. On macOS and Linux, however,
	// BinaryInstallPath points to the already-installed privileged helper. It
	// must never be treated as the bundled upgrade source.
	if runtime.GOOS == "windows" {
		installRoot := filepath.Dir(helper.BinaryInstallPath())
		candidates = append([]string{
			filepath.Join(installRoot, name),
			filepath.Join(filepath.Dir(installRoot), "resources", name),
		}, candidates...)
	}
	if cwd != "" {
		candidates = append(candidates,
			filepath.Join(cwd, "build", "bin", "resources", name),
			filepath.Join(cwd, "build", "bin", "Resources", name),
			filepath.Join(cwd, "build", "bin", name),
			filepath.Join(cwd, name),
		)
	}
	return candidates
}

func materializeBundledHelper() (string, bool, error) {
	return materializeBundledFile(helperBinaryName(helperServiceName))
}

func materializeBundledFile(name string) (string, bool, error) {
	materializeBundledMu.Lock()
	defer materializeBundledMu.Unlock()

	bundledFilesMu.RLock()
	content := bytes.Clone(bundledFiles[name])
	wantHash := bundledHashes[name]
	bundledFilesMu.RUnlock()
	if len(content) == 0 {
		return "", false, nil
	}

	dir, err := helper.UserDir()
	if err != nil {
		return "", true, err
	}
	dir = filepath.Join(dir, "helper", "resources")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", true, fmt.Errorf("create bundled helper directory: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil && runtime.GOOS != "windows" {
		return "", true, fmt.Errorf("secure bundled helper directory: %w", err)
	}

	path := filepath.Join(dir, name)
	if info, statErr := os.Stat(path); statErr == nil && info.Size() == int64(len(content)) {
		if wantHash != "" {
			if actual, hashErr := fileSHA256(path); hashErr == nil && actual == wantHash {
				if err := os.Chmod(path, 0o700); err != nil && runtime.GOOS != "windows" {
					return "", true, fmt.Errorf("make bundled helper executable: %w", err)
				}
				return path, true, nil
			}
		}
	}

	temp, err := os.CreateTemp(dir, ".kubeloop-helper-*")
	if err != nil {
		return "", true, fmt.Errorf("create bundled helper: %w", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o700); err != nil && runtime.GOOS != "windows" {
		_ = temp.Close()
		return "", true, fmt.Errorf("make temporary helper executable: %w", err)
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return "", true, fmt.Errorf("write bundled helper: %w", err)
	}
	if err := temp.Close(); err != nil {
		return "", true, fmt.Errorf("close bundled helper: %w", err)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", true, fmt.Errorf("replace bundled helper: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return "", true, fmt.Errorf("install bundled helper: %w", err)
	}
	return path, true, nil
}

func bundledHelperSHA256(source string) (string, error) {
	return bundledToolSHA256(helperBinaryName(helperServiceName), source)
}

func bundledToolSHA256(name, source string) (string, error) {
	bundledFilesMu.RLock()
	if hash := bundledHashes[name]; hash != "" {
		bundledFilesMu.RUnlock()
		return hash, nil
	}
	bundledFilesMu.RUnlock()
	return fileSHA256(source)
}

func helperBinaryName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}
