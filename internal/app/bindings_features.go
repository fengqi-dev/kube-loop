package app

import (
	"context"

	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
	"github.com/fengqi-dev/kube-loop/internal/terminal"
)

func (a *App) StartIntercept(mapping intercept.Mapping) (intercept.Info, error) {
	return a.manager.StartIntercept(a.context(), mapping)
}

func (a *App) StartMirror(mapping intercept.Mapping) (intercept.Info, error) {
	return a.manager.StartMirror(a.context(), mapping)
}

func (a *App) StopIntercept(id string) error {
	return a.manager.StopIntercept(a.context(), id)
}

func (a *App) TestIntercept(id string) session.ConnectivityTestResult {
	return a.manager.TestIntercept(a.context(), id)
}

func (a *App) ListIntercepts() []intercept.Info {
	return a.manager.ListIntercepts()
}

func (a *App) ListMirrors() []intercept.Info {
	return a.manager.ListMirrors()
}

func (a *App) StartPreview(request intercept.PreviewRequest) (intercept.Info, error) {
	return a.manager.StartPreview(a.context(), request)
}

func (a *App) StopPreview(id string) error {
	return a.manager.StopPreview(a.context(), id)
}

func (a *App) ListPreviews() []intercept.Info {
	return a.manager.ListPreviews()
}

func (a *App) StartPortForward(request portfwd.Request) (portfwd.Info, error) {
	return a.manager.StartPortForwardSession(a.context(), request)
}

func (a *App) StopPortForward(id string) error {
	return a.manager.StopPortForward(id)
}

func (a *App) TestPortForward(id string) session.ConnectivityTestResult {
	return a.manager.TestPortForward(a.context(), id)
}

func (a *App) ListPortForwards() []portfwd.Info {
	return a.manager.ListPortForwards()
}

func (a *App) EnablePodSSH(request podssh.EnableRequest) (podssh.Info, error) {
	return a.manager.EnablePodSSH(request)
}

func (a *App) DisablePodSSH(id string) error {
	return a.manager.DisablePodSSH(id)
}

func (a *App) ListPodSSH() []podssh.Info {
	return a.manager.ListPodSSH()
}

func (a *App) OpenPodSSHTerminal(id, container string) error {
	command, err := a.manager.PodSSHCommand(id, container)
	if err != nil {
		return err
	}
	if err := terminal.Open(command); err != nil {
		a.manager.AppendLog("ERROR", "open Pod SSH terminal: "+err.Error())
		return err
	}
	a.manager.AppendLog("INFO", "opened Pod SSH terminal for "+id+" container="+container)
	return nil
}

func (a *App) ResetSessions() error {
	return a.manager.ResetSessions(a.context())
}

func (a *App) SessionIntentCounts() store.SessionIntentCounts {
	return a.manager.SessionIntentCounts()
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
