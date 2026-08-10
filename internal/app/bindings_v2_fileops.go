package app

import (
	"errors"
	"strings"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/google/uuid"
)

type ServerPodFileTarget struct {
	ProfileID string `json:"profileId"`
	Pod       string `json:"pod"`
	Container string `json:"container,omitempty"`
	Path      string `json:"path"`
}

type ServerPodFileCreateRequest struct {
	ServerPodFileTarget
	Kind string `json:"kind"`
}

type ServerPodFileRenameRequest struct {
	ServerPodFileTarget
	Destination string `json:"destination"`
}

type ServerPodFileDeleteRequest struct {
	ServerPodFileTarget
	Recursive bool `json:"recursive,omitempty"`
}

func (a *App) ListServerPodFiles(target ServerPodFileTarget) (clientremote.PodFileList, error) {
	serverProfile, session, err := a.serverPodFileContext(target.ProfileID)
	if err != nil {
		return clientremote.PodFileList{}, err
	}
	return a.remote.ListPodFiles(a.context(), serverProfile, session, clientremote.PodFileSpec{
		Pod: target.Pod, Container: target.Container, Path: strings.TrimSpace(target.Path),
	})
}

func (a *App) CreateServerPodFile(request ServerPodFileCreateRequest) (clientremote.PodFileTask, error) {
	serverProfile, session, err := a.serverPodFileContext(request.ProfileID)
	if err != nil {
		return clientremote.PodFileTask{}, err
	}
	return a.remote.CreatePodFileOperation(a.context(), serverProfile, session, "create", clientremote.PodFileSpec{
		Pod: request.Pod, Container: request.Container, Path: strings.TrimSpace(request.Path), Kind: request.Kind,
	}, uuid.NewString())
}

func (a *App) RenameServerPodFile(request ServerPodFileRenameRequest) (clientremote.PodFileTask, error) {
	serverProfile, session, err := a.serverPodFileContext(request.ProfileID)
	if err != nil {
		return clientremote.PodFileTask{}, err
	}
	return a.remote.CreatePodFileOperation(a.context(), serverProfile, session, "rename", clientremote.PodFileSpec{
		Pod: request.Pod, Container: request.Container, Path: strings.TrimSpace(request.Path), Destination: strings.TrimSpace(request.Destination),
	}, uuid.NewString())
}

func (a *App) DeleteServerPodFile(request ServerPodFileDeleteRequest) (clientremote.PodFileTask, error) {
	serverProfile, session, err := a.serverPodFileContext(request.ProfileID)
	if err != nil {
		return clientremote.PodFileTask{}, err
	}
	return a.remote.CreatePodFileOperation(a.context(), serverProfile, session, "delete", clientremote.PodFileSpec{
		Pod: request.Pod, Container: request.Container, Path: strings.TrimSpace(request.Path), Recursive: request.Recursive,
	}, uuid.NewString())
}

func (a *App) serverPodFileContext(profileID string) (clientprofile.Profile, clientremote.Session, error) {
	if a.remote == nil || a.remoteSessions == nil {
		return clientprofile.Profile{}, clientremote.Session{}, errors.New("V2 remote file management is unavailable")
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	return serverProfile, session, err
}
