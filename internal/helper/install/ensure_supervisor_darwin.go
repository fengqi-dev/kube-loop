//go:build darwin

package install

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/componentstore"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperspec "github.com/fengqi-dev/kube-loop/internal/protocol/helper"
	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/google/uuid"
)

func requiresSupervisorCheck(enforceBinaryMatch bool) bool { return enforceBinaryMatch }

func installCurrentHelper(
	ctx context.Context,
	source, sourceSHA256, token string,
	uid int,
	home, singBox string,
	certificatePEM []byte,
) error {
	config := supervisor.CurrentConfig()
	supervisorSource, err := LocateBundledSupervisor()
	if err != nil {
		return fmt.Errorf("locate bundled supervisor: %w", err)
	}
	supervisorSource, err = componentstore.Cache(helper.Version, helperBinaryName(supervisorServiceName), supervisorSource)
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
	if sameInstallPath(singBox, helper.CoreInstallPath()) &&
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

	certificatePath, cleanup, err := writeTemporaryTrustedCertificate(certificatePEM)
	if err != nil {
		return err
	}
	defer cleanup()
	return ElevateSupervisorInstall(
		ctx, supervisorSource, supervisorSHA, source, sourceSHA256,
		token, uid, home, singBox, certificatePath,
	)
}

func writeTemporaryTrustedCertificate(content []byte) (string, func(), error) {
	if len(content) == 0 {
		return "", func() {}, nil
	}
	block, trailing := pem.Decode(content)
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(trailing)) != 0 {
		return "", func() {}, fmt.Errorf("traffic inspection certificate PEM is invalid")
	}
	file, err := os.CreateTemp("", "kubeloop-inspection-ca-*.pem")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temporary traffic inspection certificate: %w", err)
	}
	path := file.Name()
	cleanup := func() {
		_ = file.Close()
		_ = os.Remove(path)
	}
	if err := file.Chmod(0o600); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("secure temporary traffic inspection certificate: %w", err)
	}
	if _, err := file.Write(content); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("write temporary traffic inspection certificate: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close temporary traffic inspection certificate: %w", err)
	}
	return path, cleanup, nil
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
