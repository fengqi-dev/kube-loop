//nolint:dupl // The Wails API keeps one explicit binding per traffic type.
package app

import (
	"errors"

	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
)

func (a *App) StartServerExchange(request clientexchange.Request) (clientexchange.Info, error) {
	return startManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", request.ProfileID, request,
		func(request *clientexchange.Request, profileID string) { request.ProfileID = profileID },
	)
}

func (a *App) PauseServerExchange(profileID, taskID string) error {
	return pauseManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID, taskID,
	)
}

func (a *App) ResumeServerExchange(profileID, taskID string) (clientexchange.Info, error) {
	return resumeManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID, taskID,
	)
}

func (a *App) DeleteServerExchange(profileID, taskID string) error {
	err := deleteManagedServerTask(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID, taskID,
	)
	if errors.Is(err, clientexchange.ErrNotManagedLocally) {
		return a.DeleteServerTrafficBinding(profileID, taskID)
	}
	return err
}

func (a *App) ListServerExchanges(profileID string) ([]clientexchange.Info, error) {
	return listManagedServerTasks(
		a, a.remoteExchanges, a.remoteExchanges != nil, "Exchange is unavailable", profileID,
	)
}
