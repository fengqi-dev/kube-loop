package app

import (
	"context"
	"errors"
)

type serverLoginAttempt struct {
	cancel context.CancelFunc
}

// CancelServerLogin stops the active browser-based login, if any. It is
// intentionally idempotent so UI cleanup can call it safely while unmounting.
func (a *App) CancelServerLogin() {
	a.serverLoginMu.Lock()
	attempt := a.serverLogin
	a.serverLogin = nil
	a.serverLoginMu.Unlock()
	if attempt != nil {
		attempt.cancel()
	}
}

func (a *App) beginServerLogin() (context.Context, func(), error) {
	a.serverLoginMu.Lock()
	defer a.serverLoginMu.Unlock()
	if a.serverLogin != nil {
		return nil, nil, errors.New("a browser login is already in progress")
	}
	loginContext, cancel := context.WithCancel(a.context())
	attempt := &serverLoginAttempt{cancel: cancel}
	a.serverLogin = attempt
	return loginContext, func() {
		a.serverLoginMu.Lock()
		if a.serverLogin == attempt {
			a.serverLogin = nil
		}
		a.serverLoginMu.Unlock()
		cancel()
	}, nil
}
