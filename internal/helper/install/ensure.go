package install

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
)

var (
	ensureInstallMu sync.Mutex
)

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
	source, err := componentstore.Cache(helper.Version, helperBinaryName(helperServiceName), source)
	if err != nil {
		return fmt.Errorf("cache bundled helper: %w", err)
	}
	sourceSHA256, err := bundledHelperSHA256(source)
	if err != nil {
		return err
	}
	singBoxPath, bundled, err := materializeBundledFile(singBoxBinaryName())
	if err != nil {
		return err
	}
	if !bundled {
		singBoxPath, err = helper.LocateBundledSingBox()
		if err != nil {
			return err
		}
	}
	singBoxPath, err = componentstore.Cache(helper.Version, filepath.Base(singBoxPath), singBoxPath)
	if err != nil {
		return fmt.Errorf("cache bundled sing-box: %w", err)
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
	return waitForHelperReady(
		ctx,
		20*time.Second,
		100*time.Millisecond,
		func(pingCtx context.Context) (helperprotocol.Response, error) {
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
		},
	)
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
