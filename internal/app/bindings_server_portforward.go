package app

import (
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
)

func (a *App) StartServerPortForward(request clientportforward.Request) (clientportforward.Info, error) {
	return startManagedServerTask(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", request.ProfileID, request,
		func(request *clientportforward.Request, profileID string) { request.ProfileID = profileID },
	)
}

func (a *App) StopServerPortForward(profileID, taskID string) error {
	return stopManagedServerTask(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerPortForwards(profileID string) ([]clientportforward.Info, error) {
	return listManagedServerTasks(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID,
	)
}
