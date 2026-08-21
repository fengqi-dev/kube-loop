package app

import (
	"errors"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
)

func (a *App) StartServerFileTransfer(request clientfiletransfer.Request) (clientfiletransfer.Task, error) {
	if a.remoteFiles == nil || a.remoteSessions == nil {
		return clientfiletransfer.Task{}, errors.New("file transfer is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientfiletransfer.Task{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientfiletransfer.Task{}, err
	}
	request.ProfileID = serverProfile.ID
	return a.remoteFiles.Start(serverProfile, session, request)
}

func (a *App) ListServerFileTransfers(profileID string) ([]clientfiletransfer.Task, error) {
	if a.remoteFiles == nil {
		return nil, errors.New("file transfer is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remoteFiles.List(serverProfile.ID), nil
}

func (a *App) CancelServerFileTransfer(profileID, taskID string) error {
	if a.remoteFiles == nil {
		return errors.New("file transfer is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteFiles.Cancel(serverProfile.ID, taskID)
}

func (a *App) ResumeServerFileTransfer(profileID, taskID string) (clientfiletransfer.Task, error) {
	if a.remoteFiles == nil || a.remoteSessions == nil {
		return clientfiletransfer.Task{}, errors.New("file transfer is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientfiletransfer.Task{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientfiletransfer.Task{}, err
	}
	return a.remoteFiles.Resume(serverProfile, session, serverProfile.ID, taskID)
}

func (a *App) ClearServerFileTransferHistory(profileID string) error {
	if a.remoteFiles == nil {
		return errors.New("file transfer is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteFiles.ClearHistory(serverProfile.ID)
}

func (a *App) PickServerUploadPath(kind string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case serverFileKindFile:
		return runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{Title: "Select file to upload"})
	case serverFileKindDirectory:
		return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{Title: "Select directory to upload"})
	default:
		return "", errors.New("file transfer kind must be file or directory")
	}
}

func (a *App) PickServerDownloadPath(kind, suggestedName string) (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	name := safeDownloadName(suggestedName)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = fileTransferDirectionDownload
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case serverFileKindFile:
		return runtime.SaveFileDialog(
			a.ctx,
			runtime.SaveDialogOptions{Title: "Save downloaded file", DefaultFilename: name},
		)
	case serverFileKindDirectory:
		parent, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
			Title: "Select parent directory for download",
		})
		if err != nil || parent == "" {
			return parent, err
		}
		return filepath.Join(parent, name), nil
	default:
		return "", errors.New("file transfer kind must be file or directory")
	}
}

func safeDownloadName(value string) string {
	name := filepath.Base(strings.TrimSpace(value))
	name = strings.Map(func(character rune) rune {
		if character < 0x20 || strings.ContainsRune(`/\:*?"<>|`, character) {
			return '_'
		}
		return character
	}, name)
	name = strings.Trim(name, ". ")
	if name == "." || name == string(filepath.Separator) || name == "" {
		return fileTransferDirectionDownload
	}
	return name
}
