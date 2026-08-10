package app

import (
	"errors"

	clientpreview "github.com/fengqi-dev/kube-loop/internal/clientv2/preview"
)

func (a *App) StartServerPreview(request clientpreview.Request) (clientpreview.Info, error) {
	if a.remotePreviews == nil || a.remoteSessions == nil {
		return clientpreview.Info{}, errors.New("V2 Preview is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientpreview.Info{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientpreview.Info{}, err
	}
	request.ProfileID = serverProfile.ID
	return a.remotePreviews.Start(a.context(), serverProfile, session, request)
}

func (a *App) StopServerPreview(profileID, taskID string) error {
	if a.remotePreviews == nil {
		return errors.New("V2 Preview is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remotePreviews.Stop(a.context(), serverProfile.ID, taskID)
}

func (a *App) ListServerPreviews(profileID string) ([]clientpreview.Info, error) {
	if a.remotePreviews == nil {
		return nil, errors.New("V2 Preview is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remotePreviews.List(serverProfile.ID), nil
}
