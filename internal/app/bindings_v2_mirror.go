package app

import (
	"errors"

	clientmirror "github.com/fengqi-dev/kube-loop/internal/clientv2/mirror"
)

func (a *App) StartServerMirror(request clientmirror.Request) (clientmirror.Info, error) {
	if a.remoteMirrors == nil || a.remoteSessions == nil {
		return clientmirror.Info{}, errors.New("V2 Mirror is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientmirror.Info{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientmirror.Info{}, err
	}
	request.ProfileID = serverProfile.ID
	return a.remoteMirrors.Start(a.context(), serverProfile, session, request)
}

func (a *App) StopServerMirror(profileID, taskID string) error {
	if a.remoteMirrors == nil {
		return errors.New("V2 Mirror is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteMirrors.Stop(a.context(), serverProfile.ID, taskID)
}

func (a *App) ListServerMirrors(profileID string) ([]clientmirror.Info, error) {
	if a.remoteMirrors == nil {
		return nil, errors.New("V2 Mirror is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remoteMirrors.List(serverProfile.ID), nil
}
