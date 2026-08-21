package app

import (
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
)

func (a *App) StartServerMirror(request clientmirror.Request) (clientmirror.Info, error) {
	return startManagedServerTask(
		a, a.remoteMirrors, a.remoteMirrors != nil, "Mirror is unavailable", request.ProfileID, request,
		func(request *clientmirror.Request, profileID string) { request.ProfileID = profileID },
	)
}

func (a *App) StopServerMirror(profileID, taskID string) error {
	return stopManagedServerTask(
		a, a.remoteMirrors, a.remoteMirrors != nil, "Mirror is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerMirrors(profileID string) ([]clientmirror.Info, error) {
	return listManagedServerTasks(
		a, a.remoteMirrors, a.remoteMirrors != nil, "Mirror is unavailable", profileID,
	)
}
