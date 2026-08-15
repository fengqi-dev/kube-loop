package iam

import (
	"context"
	"errors"
	"sync"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const reloadInterval = 500 * time.Millisecond

type Loader struct {
	repositories storage.Repositories
	engine       *adminauthorization.Engine
	mu           sync.RWMutex
	lastErr      error
}

func NewLoader(repositories storage.Repositories, engine *adminauthorization.Engine) (*Loader, error) {
	if repositories == nil || engine == nil {
		return nil, errors.New("IAM authorization loader dependencies are required")
	}
	return &Loader{repositories: repositories, engine: engine}, nil
}

func (loader *Loader) Load(ctx context.Context) error {
	organizations, err := loader.repositories.Organizations().List(ctx, 2)
	if err != nil {
		return loader.fail(err)
	}
	if len(organizations) > 1 {
		return loader.fail(errors.New("single-organization IAM contains multiple organizations"))
	}
	snapshot := adminauthorization.Snapshot{Version: adminauthorization.CurrentVersion, Groups: []adminauthorization.GroupAccess{}}
	if len(organizations) == 1 {
		groups, listErr := loader.repositories.Groups().List(ctx, organizations[0].ID, storage.MaximumManagementPageFetch)
		if listErr != nil {
			return loader.fail(listErr)
		}
		for _, group := range groups {
			namespaces, namespaceErr := loader.repositories.Groups().ListNamespaces(ctx, group.ID)
			if namespaceErr != nil {
				return loader.fail(namespaceErr)
			}
			item := adminauthorization.GroupAccess{GroupID: group.ID, Administrator: group.System}
			for _, namespace := range namespaces {
				item.Namespaces = append(item.Namespaces, namespace.Namespace)
			}
			snapshot.Groups = append(snapshot.Groups, item)
		}
	}
	if err := loader.engine.Apply(snapshot); err != nil {
		return loader.fail(err)
	}
	loader.mu.Lock()
	loader.lastErr = nil
	loader.mu.Unlock()
	return nil
}

func (loader *Loader) Check(context.Context) error {
	loader.mu.RLock()
	defer loader.mu.RUnlock()
	return loader.lastErr
}

func (loader *Loader) Run(ctx context.Context) {
	ticker := time.NewTicker(reloadInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = loader.Load(ctx)
		}
	}
}

func (loader *Loader) fail(err error) error {
	loader.engine.FailClosed()
	loader.mu.Lock()
	loader.lastErr = err
	loader.mu.Unlock()
	return err
}
