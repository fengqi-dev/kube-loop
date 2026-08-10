package app

import (
	"errors"
	"strings"

	clientpodssh "github.com/fengqi-dev/kube-loop/internal/clientv2/podssh"
	"github.com/fengqi-dev/kube-loop/internal/terminal"
)

type ServerPodSSHRequest struct {
	ProfileID string `json:"profileId"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
}

func (a *App) StartServerPodSSH(request ServerPodSSHRequest) (clientpodssh.Info, error) {
	if a.remoteSSH == nil || a.remoteSessions == nil || a.remote == nil {
		return clientpodssh.Info{}, errors.New("V2 Pod SSH is unavailable")
	}
	serverProfile, err := a.serverProfile(request.ProfileID)
	if err != nil {
		return clientpodssh.Info{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientpodssh.Info{}, err
	}
	pods, err := a.remote.Pods(a.context(), serverProfile, session.Namespace)
	if err != nil {
		return clientpodssh.Info{}, err
	}
	podName := strings.TrimSpace(request.Pod)
	for _, pod := range pods {
		if pod.Name != podName || pod.Namespace != session.Namespace {
			continue
		}
		return a.remoteSSH.Start(a.context(), serverProfile, session, clientpodssh.Request{
			ProfileID: serverProfile.ID, Namespace: session.Namespace, Pod: pod.Name,
			Container: strings.TrimSpace(request.Container), PodIP: pod.PodIP,
			Ready: pod.Ready, Containers: append([]string(nil), pod.Containers...),
		})
	}
	return clientpodssh.Info{}, errors.New("Pod SSH target was not found in the active namespace")
}

func (a *App) StopServerPodSSH(profileID, endpointID string) error {
	if a.remoteSSH == nil {
		return errors.New("V2 Pod SSH is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return a.remoteSSH.Stop(serverProfile.ID, endpointID)
}

func (a *App) ListServerPodSSH(profileID string) ([]clientpodssh.Info, error) {
	if a.remoteSSH == nil {
		return nil, errors.New("V2 Pod SSH is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return a.remoteSSH.List(serverProfile.ID), nil
}

func (a *App) OpenServerPodSSH(profileID, endpointID string) error {
	items, err := a.ListServerPodSSH(profileID)
	if err != nil {
		return err
	}
	for _, item := range items {
		if item.ID == endpointID {
			return terminal.Open(item.Command)
		}
	}
	return errors.New("Pod SSH endpoint is not active")
}
