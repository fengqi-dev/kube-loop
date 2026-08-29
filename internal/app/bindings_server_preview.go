//nolint:dupl // The Wails API keeps one explicit binding per traffic type.
package app

import (
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
)

func (a *App) StartServerPreview(request clientpreview.Request) (clientpreview.Info, error) {
	return startManagedServerTask(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", request.ProfileID, request,
		func(request *clientpreview.Request, profileID string) { request.ProfileID = profileID },
	)
}

func (a *App) PauseServerPreview(profileID, taskID string) error {
	return pauseManagedServerTask(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID, taskID,
	)
}

func (a *App) ResumeServerPreview(profileID, taskID string) (clientpreview.Info, error) {
	return resumeManagedServerTask(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID, taskID,
	)
}

func (a *App) DeleteServerPreview(profileID, taskID string) error {
	return deleteManagedServerTask(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerPreviews(profileID string) ([]clientpreview.Info, error) {
	return listManagedServerTasks(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID,
	)
}
