//go:build windows

package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func RunElevatedRequest(operation, requestPath, resultPath string) error {
	err := executeElevatedRequest(operation, requestPath)
	result := elevatedResult{}
	if err != nil {
		result.Error = err.Error()
	}
	raw, marshalErr := json.Marshal(result)
	if marshalErr != nil {
		return marshalErr
	}
	if writeErr := os.WriteFile(resultPath, raw, 0o600); writeErr != nil {
		return fmt.Errorf("write elevated result: %w", writeErr)
	}
	return err
}

func executeElevatedRequest(operation, requestPath string) error {
	raw, err := os.ReadFile(requestPath)
	if err != nil {
		return fmt.Errorf("read elevated request: %w", err)
	}
	var request elevatedRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return fmt.Errorf("decode elevated request: %w", err)
	}
	if request.ExpectedSHA256 == "" {
		return fmt.Errorf("expected helper SHA-256 is required")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find elevated helper executable: %w", err)
	}
	actual, err := fileSHA256(executable)
	if err != nil {
		return fmt.Errorf("hash elevated helper executable: %w", err)
	}
	if !strings.EqualFold(actual, request.ExpectedSHA256) {
		return fmt.Errorf("elevated helper checksum mismatch")
	}
	switch operation {
	case "install":
		return elevatedInstall(request)
	case "uninstall":
		return UninstallFromCLI()
	default:
		return fmt.Errorf("unsupported elevated operation %q", operation)
	}
}

func elevatedInstall(request elevatedRequest) error {
	source := request.ServiceSource
	if source == "" {
		executable, err := os.Executable()
		if err != nil {
			return fmt.Errorf("find elevated install tool: %w", err)
		}
		executable, err = filepath.EvalSymlinks(executable)
		if err != nil {
			return fmt.Errorf("resolve elevated install tool: %w", err)
		}
		source = filepath.Join(filepath.Dir(executable), "kubeloop-helper.exe")
	}
	source, err := filepath.EvalSymlinks(source)
	if err != nil {
		return fmt.Errorf("resolve helper service source: %w", err)
	}
	if request.ServiceSHA256 != "" {
		actual, hashErr := fileSHA256(source)
		if hashErr != nil {
			return fmt.Errorf("hash helper service source: %w", hashErr)
		}
		if !strings.EqualFold(actual, request.ServiceSHA256) {
			return fmt.Errorf("bundled helper checksum mismatch")
		}
	}
	return InstallFromCLI(
		source,
		request.Token,
		request.UID,
		request.Version,
		request.HomeDir,
		request.OwnerSID,
		request.SingBoxPath,
	)
}
