package app

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) PickLocalDirectory() (string, error) {
	if a.ctx == nil {
		return "", errors.New("application is not ready")
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select local directory",
	})
}

func (a *App) LocalHomeDirectory() (string, error) {
	return a.fileManager.LocalHomeDirectory()
}

func (a *App) ListLocalDirectory(path string) ([]filemanager.FileEntry, error) {
	return a.fileManager.ListLocalDirectory(path)
}

func (a *App) ListPodDirectory(
	target filemanager.Target,
	path string,
) ([]filemanager.FileEntry, error) {
	return a.fileManager.ListPodDirectory(a.context(), target, path)
}

func (a *App) CreateLocalDirectory(parent, name string) error {
	return a.fileManager.CreateLocalDirectory(parent, name)
}

func (a *App) CreateLocalFile(parent, name string) error {
	return a.fileManager.CreateLocalFile(parent, name)
}

func (a *App) CreatePodDirectory(
	target filemanager.Target,
	parent, name string,
) error {
	return a.fileManager.CreatePodDirectory(a.context(), target, parent, name)
}

func (a *App) CreatePodFile(
	target filemanager.Target,
	parent, name string,
) error {
	return a.fileManager.CreatePodFile(a.context(), target, parent, name)
}

func (a *App) RenameLocalPath(path, newName string) error {
	return a.fileManager.RenameLocalPath(path, newName)
}

func (a *App) RenamePodPath(
	target filemanager.Target,
	path, newName string,
) error {
	return a.fileManager.RenamePodPath(a.context(), target, path, newName)
}

func (a *App) DeleteLocalPath(path string) error {
	return a.fileManager.DeleteLocalPath(path)
}

func (a *App) DeletePodPath(target filemanager.Target, path string) error {
	return a.fileManager.DeletePodPath(a.context(), target, path)
}

func (a *App) StartFileTransfer(
	request filemanager.TransferRequest,
) (filemanager.TransferTask, error) {
	return a.fileManager.Start(a.context(), request)
}

func (a *App) ListFileTransfers() []filemanager.TransferTask {
	return a.fileManager.ListTransfers()
}

func (a *App) PauseFileTransfer(id string) error {
	return a.fileManager.Pause(id)
}

func (a *App) ResumeFileTransfer(id string) error {
	return a.fileManager.Resume(id)
}

func (a *App) CancelFileTransfer(id string) error {
	return a.fileManager.Cancel(id)
}

func (a *App) ClearFileTransferHistory() error {
	return a.fileManager.ClearHistory()
}
