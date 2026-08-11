package app

import (
	"errors"

	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
)

func (a *App) StartServerPortForward(request clientportforward.Request) (clientportforward.Info, error) {
	if a.remoteForwards == nil || a.remoteSessions == nil {
		return clientportforward.Info{}, errors.New("Port Forward is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientportforward.Info{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientportforward.Info{}, err
	}
	request.ProfileID = serverProfile.ID
	return a.remoteForwards.Start(a.context(), serverProfile, session, request)
}

func (a *App) StopServerPortForward(profileID, taskID string) error {
	if a.remoteForwards == nil {
		return errors.New("Port Forward is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteForwards.Stop(a.context(), serverProfile.ID, taskID)
}

func (a *App) TestServerPortForward(profileID, taskID string) error {
	if a.remoteForwards == nil {
		return errors.New("Port Forward is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteForwards.Test(a.context(), serverProfile.ID, taskID)
}

func (a *App) ListServerPortForwards(profileID string) ([]clientportforward.Info, error) {
	if a.remoteForwards == nil {
		return nil, errors.New("Port Forward is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remoteForwards.List(serverProfile.ID), nil
}
