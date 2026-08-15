package main

import (
	"context"
	"errors"
	"os"
	"strings"

	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func initializeLocalUsers(ctx context.Context, store *controlplanestorage.Store) (*adminlocaluser.Service, error) {
	_ = ctx
	return adminlocaluser.New(store)
}

func readInitialPasswordFile(path string) ([]byte, error) {
	value, err := os.ReadFile(strings.TrimSpace(path))
	if err != nil {
		return nil, errors.New("read initial administrator password Secret")
	}
	if len(value) < 12 || len(value) > 1024 {
		clear(value)
		return nil, errors.New("initial administrator password Secret has an invalid length")
	}
	return value, nil
}
