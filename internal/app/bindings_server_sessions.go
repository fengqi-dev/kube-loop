package app

import (
	"errors"

	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

// ListServerSessions reads the Session inventory from TrafficBinding CRDs.
// Runtime manager maps and database Task lists are not consulted.
func (a *App) ListServerSessions(profileID string) ([]clientremote.TrafficBindingSession, error) {
	if a.remote == nil || a.remoteSessions == nil {
		return nil, errors.New("TrafficBinding Sessions are unavailable")
	}
	serverProfile, session, err := a.activeServerTask(profileID)
	if err != nil {
		return nil, err
	}
	return a.remote.ListTrafficBindings(a.context(), serverProfile, session)
}
