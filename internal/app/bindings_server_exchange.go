package app

import (
	"errors"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
)

func (a *App) StartServerExchange(request clientexchange.Request) (clientexchange.Info, error) {
	if a.remoteExchanges == nil || a.remoteSessions == nil {
		return clientexchange.Info{}, errors.New("Exchange is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientexchange.Info{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientexchange.Info{}, err
	}
	request.ProfileID = serverProfile.ID
	return a.remoteExchanges.Start(a.context(), serverProfile, session, request)
}

func (a *App) StopServerExchange(profileID, taskID string) error {
	if a.remoteExchanges == nil {
		return errors.New("Exchange is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteExchanges.Stop(a.context(), serverProfile.ID, taskID)
}

func (a *App) ListServerExchanges(profileID string) ([]clientexchange.Info, error) {
	if a.remoteExchanges == nil {
		return nil, errors.New("Exchange is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remoteExchanges.List(serverProfile.ID), nil
}
