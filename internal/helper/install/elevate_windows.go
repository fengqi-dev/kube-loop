//go:build windows

package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/helper"
	"golang.org/x/sys/windows"
)

const elevatedResultPollInterval = 100 * time.Millisecond

type elevatedRequest struct {
	ExpectedSHA256 string `json:"expectedSha256,omitempty"`
	ServiceSource  string `json:"serviceSource,omitempty"`
	ServiceSHA256  string `json:"serviceSha256,omitempty"`
	SingBoxPath    string `json:"singBoxPath,omitempty"`
	Token          string `json:"token,omitempty"`
	UID            int    `json:"uid,omitempty"`
	Version        string `json:"version,omitempty"`
	HomeDir        string `json:"homeDir,omitempty"`
	OwnerSID       string `json:"ownerSid,omitempty"`
}

type elevatedResult struct {
	Error string `json:"error,omitempty"`
}

func ElevateInstall(
	ctx context.Context,
	source, expectedSHA256, token string,
	uid int,
	homeDir, singBoxPath string,
) error {
	ownerSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("find current Windows user SID: %w", err)
	}
	installTool, err := LocateBundledInstallTool()
	if err != nil {
		return err
	}
	return runElevatedTool(ctx, installTool, elevatedRequest{
		ServiceSource: source,
		ServiceSHA256: expectedSHA256,
		SingBoxPath:   singBoxPath,
		Token:         token,
		UID:           uid,
		Version:       helper.Version,
		HomeDir:       homeDir,
		OwnerSID:      ownerSID,
	})
}

func ElevateUninstall(ctx context.Context, source string) error {
	_ = source
	uninstallTool, err := LocateBundledUninstallTool()
	if err != nil {
		return err
	}
	return runElevatedTool(ctx, uninstallTool, elevatedRequest{})
}

func runElevatedTool(ctx context.Context, tool string, request elevatedRequest) error {
	lockedTool, toolHash, err := lockAndHashElevatedSource(tool)
	if err != nil {
		return err
	}
	defer lockedTool.Close()
	request.ExpectedSHA256 = toolHash
	if request.ServiceSource != "" {
		lockedService, lockErr := lockAndVerifyElevatedSource(request.ServiceSource, request.ServiceSHA256)
		if lockErr != nil {
			return lockErr
		}
		defer lockedService.Close()
	}
	ownerSID, err := currentUserSID()
	if err != nil {
		return fmt.Errorf("find current Windows user SID: %w", err)
	}
	requestPath, err := createElevatedExchangeFile("kubeloop-elevated-request-*.json", ownerSID)
	if err != nil {
		return fmt.Errorf("create elevated request: %w", err)
	}
	defer os.Remove(requestPath)
	resultPath, err := createElevatedExchangeFile("kubeloop-elevated-result-*.json", ownerSID)
	if err != nil {
		return fmt.Errorf("create elevated result: %w", err)
	}
	defer os.Remove(resultPath)

	raw, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode elevated request: %w", err)
	}
	if err := os.WriteFile(requestPath, raw, 0o600); err != nil {
		return fmt.Errorf("write elevated request: %w", err)
	}
	if err := os.WriteFile(resultPath, nil, 0o600); err != nil {
		return fmt.Errorf("clear elevated result: %w", err)
	}

	args := strings.Join([]string{
		syscall.EscapeArg("--request"),
		syscall.EscapeArg(requestPath),
		syscall.EscapeArg("--result"),
		syscall.EscapeArg(resultPath),
	}, " ")
	verbPtr, _ := windows.UTF16PtrFromString("runas")
	toolPtr, err := windows.UTF16PtrFromString(tool)
	if err != nil {
		return fmt.Errorf("encode elevated tool path: %w", err)
	}
	argsPtr, err := windows.UTF16PtrFromString(args)
	if err != nil {
		return fmt.Errorf("encode elevated tool arguments: %w", err)
	}
	cwdPtr, err := windows.UTF16PtrFromString(filepath.Dir(tool))
	if err != nil {
		return fmt.Errorf("encode elevated tool directory: %w", err)
	}
	if err := windows.ShellExecute(
		0, verbPtr, toolPtr, argsPtr, cwdPtr, windows.SW_HIDE,
	); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fmt.Errorf("Windows elevation was cancelled")
		}
		return fmt.Errorf("launch elevated helper tool: %w", err)
	}
	return waitElevatedResult(ctx, resultPath)
}

func lockAndVerifyElevatedSource(source, expectedSHA256 string) (*os.File, error) {
	if expectedSHA256 == "" {
		return nil, fmt.Errorf("expected helper SHA-256 is required")
	}
	file, actual, err := lockAndHashElevatedSource(source)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(actual, expectedSHA256) {
		_ = file.Close()
		return nil, fmt.Errorf("bundled helper checksum mismatch")
	}
	return file, nil
}

func lockAndHashElevatedSource(source string) (*os.File, string, error) {
	sourcePtr, err := windows.UTF16PtrFromString(source)
	if err != nil {
		return nil, "", fmt.Errorf("encode elevated helper path: %w", err)
	}
	handle, err := windows.CreateFile(
		sourcePtr,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, "", fmt.Errorf("lock elevated helper source: %w", err)
	}
	file := os.NewFile(uintptr(handle), source)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, "", fmt.Errorf("open locked elevated helper source")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return nil, "", fmt.Errorf("hash locked elevated helper source: %w", err)
	}
	return file, hex.EncodeToString(hash.Sum(nil)), nil
}

func createElevatedExchangeFile(pattern, ownerSID string) (string, error) {
	file, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := helper.ConfigureElevatedExchangeAccess(path, ownerSID); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

func waitElevatedResult(ctx context.Context, path string) error {
	ticker := time.NewTicker(elevatedResultPollInterval)
	defer ticker.Stop()
	for {
		raw, err := os.ReadFile(path)
		if err == nil && len(raw) != 0 {
			var result elevatedResult
			if decodeErr := json.Unmarshal(raw, &result); decodeErr != nil {
				return fmt.Errorf("decode elevated helper result: %w", decodeErr)
			}
			if result.Error != "" {
				return fmt.Errorf("elevated helper command: %s", result.Error)
			}
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("read elevated helper result: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
