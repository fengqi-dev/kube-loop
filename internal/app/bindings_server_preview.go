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

func (a *App) StopServerPreview(profileID, taskID string) error {
	return stopManagedServerTask(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerPreviews(profileID string) ([]clientpreview.Info, error) {
	return listManagedServerTasks(
		a, a.remotePreviews, a.remotePreviews != nil, "Preview is unavailable", profileID,
	)
}
