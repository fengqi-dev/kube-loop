package app

import (
	"errors"
	"strings"

	clientremote "github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
)

type ServerExecRequest struct {
	ProfileID string   `json:"profileId"`
	Pod       string   `json:"pod"`
	Container string   `json:"container,omitempty"`
	Command   []string `json:"command"`
	TTY       bool     `json:"tty"`
	Width     uint16   `json:"width,omitempty"`
	Height    uint16   `json:"height,omitempty"`
}

func (a *App) StartServerExec(request ServerExecRequest) (clientremote.ExecTask, error) {
	if a.remoteExecs == nil || a.remoteSessions == nil {
		return clientremote.ExecTask{}, errors.New("V2 Pod exec is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientremote.ExecTask{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientremote.ExecTask{}, err
	}
	task, err := a.remoteExecs.Start(a.context(), serverProfile, session, clientremote.ExecSpec{
		Pod: strings.TrimSpace(request.Pod), Container: strings.TrimSpace(request.Container),
		Command: append([]string(nil), request.Command...), TTY: request.TTY,
	})
	if err != nil {
		return clientremote.ExecTask{}, err
	}
	if request.Width != 0 || request.Height != 0 {
		if request.Width == 0 || request.Height == 0 {
			_ = a.remoteExecs.Stop(serverProfile.ID, task.ID)
			return clientremote.ExecTask{}, errors.New("Pod exec terminal size is incomplete")
		}
		if err := a.remoteExecs.Resize(a.context(), serverProfile.ID, task.ID, request.Width, request.Height); err != nil {
			_ = a.remoteExecs.Stop(serverProfile.ID, task.ID)
			return clientremote.ExecTask{}, err
		}
	}
	return task, nil
}

func (a *App) WriteServerExecInput(profileID, taskID, input string) error {
	if a.remoteExecs == nil {
		return errors.New("V2 Pod exec is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	if len(input) > 64<<10 {
		return errors.New("Pod exec input exceeds 64 KiB")
	}
	return a.remoteExecs.Write(a.context(), serverProfile.ID, taskID, []byte(input))
}

func (a *App) ResizeServerExec(profileID, taskID string, width, height uint16) error {
	if a.remoteExecs == nil {
		return errors.New("V2 Pod exec is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteExecs.Resize(a.context(), serverProfile.ID, taskID, width, height)
}

func (a *App) StopServerExec(profileID, taskID string) error {
	if a.remoteExecs == nil {
		return errors.New("V2 Pod exec is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteExecs.Stop(serverProfile.ID, taskID)
}
