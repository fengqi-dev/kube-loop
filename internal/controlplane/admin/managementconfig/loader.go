package managementconfig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const DefaultPolicyReloadInterval = 500 * time.Millisecond

var ErrPolicyUnavailable = errors.New("management policy is unavailable")

// PolicyLoader verifies the immutable policy aggregate before installing its
// active policy. Any read or consistency failure immediately removes all
// database-backed grants from the in-memory authorizer.
type PolicyLoader struct {
	repositories storage.Repositories
	engine       *adminauthorization.Engine
	interval     time.Duration

	mu             sync.RWMutex
	lastErr        error
	loadedObjectID string
}

func NewPolicyLoader(
	repositories storage.Repositories,
	engine *adminauthorization.Engine,
	interval time.Duration,
) (*PolicyLoader, error) {
	if repositories == nil || engine == nil {
		return nil, errors.New("management policy loader dependencies are required")
	}
	if interval == 0 {
		interval = DefaultPolicyReloadInterval
	}
	if interval < 100*time.Millisecond || interval > time.Minute {
		return nil, errors.New("management policy reload interval must be between 100ms and 1m")
	}
	return &PolicyLoader{repositories: repositories, engine: engine, interval: interval}, nil
}

func (loader *PolicyLoader) Load(ctx context.Context) error {
	active, err := loader.repositories.ActiveManagementConfigs().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	)
	if errors.Is(err, storage.ErrNotFound) && loader.loadedObjectID == "" {
		loader.setError(nil)
		return nil
	}
	if err != nil {
		return loader.fail(fmt.Errorf("%w: read active policy", ErrPolicyUnavailable))
	}
	if active.ObjectID == loader.loadedObjectID && loader.engine.Available() {
		loader.setError(nil)
		return nil
	}

	config, err := loader.repositories.AdminPolicyConfigs().Get(ctx, active.ObjectID)
	if err != nil {
		return loader.fail(fmt.Errorf("%w: read policy configuration", ErrPolicyUnavailable))
	}
	if config.ValidationState != storage.ConfigValidationValid || config.SpecHash != policySpecHash(config.Spec) {
		return loader.fail(fmt.Errorf("%w: policy configuration failed verification", ErrPolicyUnavailable))
	}
	snapshot, err := decodePolicySpec(config.Spec)
	if err != nil {
		return loader.fail(err)
	}
	if err := loader.engine.Apply(snapshot); err != nil {
		return loader.fail(fmt.Errorf("%w: compile active policy", ErrPolicyUnavailable))
	}
	loader.loadedObjectID = active.ObjectID
	loader.setError(nil)
	return nil
}

func (loader *PolicyLoader) Run(ctx context.Context) {
	ticker := time.NewTicker(loader.interval)
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

func (loader *PolicyLoader) Check(context.Context) error {
	loader.mu.RLock()
	defer loader.mu.RUnlock()
	return loader.lastErr
}

func (loader *PolicyLoader) fail(err error) error {
	loader.engine.FailClosed()
	loader.setError(err)
	return err
}

func (loader *PolicyLoader) setError(err error) {
	loader.mu.Lock()
	loader.lastErr = err
	loader.mu.Unlock()
}

func decodePolicySpec(spec json.RawMessage) (adminauthorization.Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(spec))
	decoder.DisallowUnknownFields()
	var value struct {
		Version  int                                 `json:"version"`
		Roles    []adminauthorization.RoleDefinition `json:"roles,omitempty"`
		Bindings []adminauthorization.Binding        `json:"bindings"`
	}
	if err := decoder.Decode(&value); err != nil {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: decode policy configuration", ErrPolicyUnavailable)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: policy configuration has trailing content", ErrPolicyUnavailable)
	}
	snapshot := adminauthorization.Snapshot{Version: value.Version, Roles: value.Roles, Bindings: value.Bindings}
	if _, err := adminauthorization.New(snapshot); err != nil {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: validate policy configuration", ErrPolicyUnavailable)
	}
	return snapshot, nil
}

func policySpecHash(spec json.RawMessage) string {
	digest := sha256.Sum256(spec)
	return hex.EncodeToString(digest[:])
}
