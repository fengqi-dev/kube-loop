//nolint:dupl // The Wails API keeps one explicit binding per traffic type.
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

func (a *App) PauseServerPortForward(profileID, taskID string) error {
	return pauseManagedServerTask(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID, taskID,
	)
}

func (a *App) ResumeServerPortForward(profileID, taskID string) (clientportforward.Info, error) {
	return resumeManagedServerTask(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID, taskID,
	)
}

func (a *App) DeleteServerPortForward(profileID, taskID string) error {
	return deleteManagedServerTask(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerPortForwards(profileID string) ([]clientportforward.Info, error) {
	return listManagedServerTasks(
		a, a.remoteForwards, a.remoteForwards != nil, "Port Forward is unavailable", profileID,
	)
}
