//go:build darwin

package install

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperspec "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/google/uuid"
)

func requiresSupervisorCheck(enforceBinaryMatch bool) bool { return enforceBinaryMatch }

func installCurrentHelper(ctx context.Context, source, sourceSHA256, token string, uid int, home, singBox string) error {
	config := supervisor.CurrentConfig()
	supervisorSource, err := LocateBundledSupervisor()
	if err != nil {
		return fmt.Errorf("locate bundled supervisor: %w", err)
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
	if statusErr == nil && status.Protocol == supervisorprotocol.Version && status.Channel == config.Channel && installedSupervisorSHA == supervisorSHA {
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

	return ElevateSupervisorInstall(ctx, supervisorSource, supervisorSHA, source, sourceSHA256, token, uid, home, singBox)
}
