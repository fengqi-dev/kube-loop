//go:build darwin

package install

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperspec "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
)

func requiresSupervisorCheck(enforceBinaryMatch bool) bool { return enforceBinaryMatch }

func installCurrentHelper(
	ctx context.Context,
	source, sourceSHA256, token string,
	uid int,
	home, singBox string,
	_ []byte,
) error {
	config := supervisor.CurrentConfig()
	supervisorSource, err := LocateBundledSupervisor()
	if err != nil {
		return fmt.Errorf("locate bundled supervisor: %w", err)
	}
	supervisorSource, err = componentstore.Cache(
		helper.Version,
		helperBinaryName(supervisorServiceName),
		supervisorSource,
	)
	if err != nil {
		return fmt.Errorf("cache bundled supervisor: %w", err)
	}
	supervisorSHA, err := bundledToolSHA256(helperBinaryName(supervisorServiceName), supervisorSource)
	if err != nil {
		return fmt.Errorf("hash bundled supervisor: %w", err)
	}
	installedSupervisorSHA, _ := fileSHA256(config.BinaryPath)
	client := &supervisor.Client{Config: config, Token: token}
	statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	status, statusErr := client.Status(statusCtx)
	cancel()
	if installedCoreMatches(singBox, helper.CoreInstallPath()) &&
		canUpdateWorkerThroughSupervisor(status, statusErr, config.Channel, installedSupervisorSHA, supervisorSHA) {
		if status.Worker.SHA256 == sourceSHA256 && status.Worker.Version == helper.Version &&
			status.Worker.Protocol == helperspec.Version && status.Worker.CoreReady {
			return nil
		}
		info, err := os.Stat(source)
		if err != nil {
			return fmt.Errorf("stat bundled worker: %w", err)
		}
		manifest := supervisorprotocol.UpdateManifest{
			SchemaVersion: supervisorprotocol.SchemaVersion,
			RequestID:     uuid.NewString(), Channel: config.Channel,
			Version: helper.Version, WorkerProtocol: helperspec.Version,
			MinimumSupervisorProtocol: supervisorprotocol.Version,
			Size:                      info.Size(), SHA256: sourceSHA256,
		}
		_, err = client.UpdateWorker(ctx, manifest, source)
		return err
	}

	return ElevateSupervisorInstall(
		ctx, supervisorSource, supervisorSHA, source, sourceSHA256, helper.Version, config.Channel,
		token, uid, home, singBox,
	)
}

func installedCoreMatches(source, installed string) bool {
	// The component cache is only a distribution source, so its path must differ
	// from the protected system copy even when both contain the same core.
	needsUpdate, err := helperNeedsBinaryUpdate(source, installed)
	return err == nil && !needsUpdate
}

func canUpdateWorkerThroughSupervisor(
	status supervisorprotocol.Response,
	statusErr error,
	channel string,
	installedSupervisorSHA string,
	bundledSupervisorSHA string,
) bool {
	return statusErr == nil &&
		status.Protocol == supervisorprotocol.Version &&
		status.Channel == channel &&
		installedSupervisorSHA == bundledSupervisorSHA &&
		status.Worker.Installed &&
		status.Worker.Running
}
