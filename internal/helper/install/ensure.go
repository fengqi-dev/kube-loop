package install

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/helperrpc"
)

var (
	ensureInstallMu sync.Mutex
)

// EnsureInstall makes the helper available for automatic TUN startup. Release
// builds always require the installed binary to match the bundled helper. Dev
// builds reuse a healthy, protocol-compatible helper so wails dev does not
// request administrator authorization after every helper rebuild.
func EnsureInstall(ctx context.Context) error {
	return ensureInstall(ctx, false)
}

// EnsureCurrentInstall installs or upgrades to the exact bundled helper. Use it
// for explicit user-driven installs and E2E setup where binary drift must not be
// accepted, including in development builds.
func EnsureCurrentInstall(ctx context.Context) error {
	return ensureInstall(ctx, true)
}

func ensureInstall(ctx context.Context, requireCurrentBinary bool) error {
	ensureInstallMu.Lock()
	defer ensureInstallMu.Unlock()

	decision, err := decideHelperInstall(ctx, requireCurrentBinary)
	if err != nil {
		return err
	}
	if !decision.required {
		return nil
	}
	artifacts, err := prepareHelperInstall(decision.source)
	if err != nil {
		return err
	}
	if err := installCurrentHelper(
		ctx,
		artifacts.helperPath,
		artifacts.helperSHA256,
		artifacts.token,
		currentUID(),
		artifacts.home,
		artifacts.singBoxPath,
	); err != nil {
		return err
	}
	return waitForInstalledHelper(ctx, artifacts.token)
}

type helperInstallDecision struct {
	source   string
	required bool
}

func decideHelperInstall(ctx context.Context, requireCurrentBinary bool) (helperInstallDecision, error) {
	status := helper.GetStatus(ctx)
	enforceBinaryMatch := mustMatchBundledHelper(requireCurrentBinary, helper.IsDevBuild())
	helperHealthy := status.Running && status.CoreReady &&
		status.Version == helper.Version && status.Protocol == helperrpc.Version
	if helperHealthy && !enforceBinaryMatch && !coreNeedsUpdate() {
		return helperInstallDecision{}, nil
	}
	source, locateErr := LocateBundledHelper()
	needsBinaryUpdate := false
	if locateErr == nil {
		var hashErr error
		needsBinaryUpdate, hashErr = helperNeedsBinaryUpdate(source, helper.BinaryInstallPath())
		if hashErr != nil {
			return helperInstallDecision{}, hashErr
		}
	}
	if canReuseInstalledHelper(
		status, helper.Version, helperrpc.Version, enforceBinaryMatch, needsBinaryUpdate,
	) && !requiresSupervisorCheck(enforceBinaryMatch) {
		return helperInstallDecision{}, nil
	}
	if locateErr != nil {
		return helperInstallDecision{}, locateErr
	}
	return helperInstallDecision{source: source, required: true}, nil
}

type helperInstallArtifacts struct {
	helperPath   string
	helperSHA256 string
	singBoxPath  string
	token        string
	home         string
}

func prepareHelperInstall(source string) (helperInstallArtifacts, error) {
	artifacts := helperInstallArtifacts{}
	var err error
	artifacts.helperPath, err = componentstore.Cache(
		helper.Version,
		helperBinaryName(helperServiceName),
		source,
	)
	if err != nil {
		return artifacts, fmt.Errorf("cache bundled helper: %w", err)
	}
	artifacts.helperSHA256, err = bundledHelperSHA256(artifacts.helperPath)
	if err != nil {
		return artifacts, err
	}
	singBoxPath, bundled, err := materializeBundledFile(singBoxBinaryName())
	if err != nil {
		return artifacts, err
	}
	if !bundled {
		singBoxPath, err = helper.LocateBundledSingBox()
		if err != nil {
			return artifacts, err
		}
	}
	artifacts.singBoxPath, err = componentstore.Cache(helper.Version, filepath.Base(singBoxPath), singBoxPath)
	if err != nil {
		return artifacts, fmt.Errorf("cache bundled sing-box: %w", err)
	}
	artifacts.token, err = helper.EnsureUserToken()
	if err != nil {
		return artifacts, err
	}
	artifacts.home, err = helper.UserHomeDir()
	if err != nil {
		return artifacts, err
	}
	return artifacts, nil
}

func mustMatchBundledHelper(requireCurrentBinary, developmentBuild bool) bool {
	return requireCurrentBinary || !developmentBuild
}

// coreNeedsUpdate reports whether the sing-box binary installed at CoreInstallPath
// differs from the bundled copy (build/bin/sing-box). When the helper is healthy
// and reused in dev builds, this check ensures a fresh sing-box build is picked up
// without requiring a full helper reinstall.
func coreNeedsUpdate() bool {
	bundled, err := helper.LocateBundledSingBox()
	if err != nil {
		return true
	}
	installed := helper.CoreInstallPath()
	needsUpdate, err := helperNeedsBinaryUpdate(bundled, installed)
	if err != nil {
		return true
	}
	return needsUpdate
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
