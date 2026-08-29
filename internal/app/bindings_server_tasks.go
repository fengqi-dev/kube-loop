package app

import (
	"context"
	"errors"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

type serverTaskManager[Request, Info any] interface {
	Start(context.Context, clientprofile.Profile, clientremote.Session, Request) (Info, error)
	Pause(context.Context, string, string) error
	Resume(context.Context, string, string) (Info, error)
	Delete(context.Context, string, string) error
	List(string) []Info
}

func (a *App) activeServerTask(profileID string) (clientprofile.Profile, clientremote.Session, error) {
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, err
	}
	session, err := a.remoteSessions.Current(serverProfile.ID)
	if err != nil {
		return clientprofile.Profile{}, clientremote.Session{}, err
	}
	return serverProfile, session, nil
}

func startServerTask[T any](
	a *App,
	profileID,
	unavailable string,
	available bool,
	start func(clientprofile.Profile, clientremote.Session) (T, error),
) (T, error) {
	if !available || a.remoteSessions == nil {
		var zero T
		return zero, errors.New(unavailable)
	}
	serverProfile, session, err := a.activeServerTask(profileID)
	if err != nil {
		var zero T
		return zero, err
	}
	return start(serverProfile, session)
}

func stopServerTask(
	a *App,
	profileID,
	unavailable string,
	available bool,
	stop func(string) error,
) error {
	if !available {
		return errors.New(unavailable)
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return err
	}
	return stop(serverProfile.ID)
}

func listServerTasks[T any](
	a *App,
	profileID,
	unavailable string,
	available bool,
	list func(string) []T,
) ([]T, error) {
	if !available {
		return nil, errors.New(unavailable)
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		return nil, err
	}
	return list(serverProfile.ID), nil
}

func startManagedServerTask[Request, Info any](
	a *App,
	manager serverTaskManager[Request, Info],
	available bool,
	unavailable, profileID string,
	request Request,
	setProfileID func(*Request, string),
) (Info, error) {
	return startServerTask(a, profileID, unavailable, available,
		func(serverProfile clientprofile.Profile, session clientremote.Session) (Info, error) {
			setProfileID(&request, serverProfile.ID)
			return manager.Start(a.context(), serverProfile, session, request)
		})
}

func pauseManagedServerTask[Request, Info any](
	a *App,
	manager serverTaskManager[Request, Info],
	available bool,
	unavailable, profileID, taskID string,
) error {
	return stopServerTask(a, profileID, unavailable, available,
		func(id string) error { return manager.Pause(a.context(), id, taskID) })
}

func resumeManagedServerTask[Request, Info any](
	a *App,
	manager serverTaskManager[Request, Info],
	available bool,
	unavailable, profileID, taskID string,
) (Info, error) {
	if !available {
		var zero Info
		return zero, errors.New(unavailable)
	}
	serverProfile, err := a.serverProfile(profileID)
	if err != nil {
		var zero Info
		return zero, err
	}
	return manager.Resume(a.context(), serverProfile.ID, taskID)
}

func deleteManagedServerTask[Request, Info any](
	a *App,
	manager serverTaskManager[Request, Info],
	available bool,
	unavailable, profileID, taskID string,
) error {
	return stopServerTask(a, profileID, unavailable, available,
		func(id string) error { return manager.Delete(a.context(), id, taskID) })
}

func listManagedServerTasks[Request, Info any](
	a *App,
	manager serverTaskManager[Request, Info],
	available bool,
	unavailable, profileID string,
) ([]Info, error) {
	return listServerTasks(a, profileID, unavailable, available, manager.List)
}
