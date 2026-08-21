package app

import (
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
)

func (a *App) StartServerExchange(request clientexchange.Request) (clientexchange.Info, error) {
	return startManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", request.ProfileID, request,
		func(request *clientexchange.Request, profileID string) { request.ProfileID = profileID },
	)
}

func (a *App) StopServerExchange(profileID, taskID string) error {
	return stopManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID, taskID,
	)
}

func (a *App) ListServerExchanges(profileID string) ([]clientexchange.Info, error) {
	return listManagedServerTasks(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID,
	)
}
