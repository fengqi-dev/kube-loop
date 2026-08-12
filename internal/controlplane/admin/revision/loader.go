package revision

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"sync"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

const DefaultPolicyReloadInterval = 2 * time.Second

var ErrPolicyUnavailable = errors.New("management policy is unavailable")

// PolicyLoader verifies the immutable policy aggregate before installing its
// active revision. Any read or consistency failure immediately removes all
// database-backed grants from the in-memory authorizer.
type PolicyLoader struct {
	repositories storage.Repositories
	engine       *adminauthorization.Engine
	interval     time.Duration

	mu      sync.RWMutex
	lastErr error
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
	active, err := loader.repositories.ActiveManagementRevisions().Get(
		ctx, storage.ManagementConfigurationPolicy, storage.ManagementPolicyID,
	)
	if errors.Is(err, storage.ErrNotFound) && loader.engine.ETag() == 0 {
		loader.setError(nil)
		return nil
	}
	if err != nil {
		return loader.fail(fmt.Errorf("%w: read active revision", ErrPolicyUnavailable))
	}
	if active.ETag < loader.engine.ETag() ||
		active.ETag == loader.engine.ETag() && active.Revision != loader.engine.Revision() {
		return loader.fail(fmt.Errorf("%w: active pointer moved backwards", ErrPolicyUnavailable))
	}
	if active.ETag == loader.engine.ETag() && loader.engine.Available() {
		loader.setError(nil)
		return nil
	}

	revision, err := loader.repositories.AdminPolicyRevisions().Get(ctx, active.Revision)
	if err != nil {
		return loader.fail(fmt.Errorf("%w: read immutable revision", ErrPolicyUnavailable))
	}
	if revision.ValidationState != storage.RevisionValidationValid || revision.SpecHash != policySpecHash(revision.Spec) {
		return loader.fail(fmt.Errorf("%w: immutable revision failed verification", ErrPolicyUnavailable))
	}
	snapshot, err := decodePolicySpec(revision.Spec, revision.Revision)
	if err != nil {
		return loader.fail(err)
	}
	assignments, err := loader.repositories.AdminAssignments().ListByPolicyRevision(ctx, revision.Revision)
	if err != nil {
		return loader.fail(fmt.Errorf("%w: read policy assignments", ErrPolicyUnavailable))
	}
	stored, err := assignmentSnapshot(assignments, revision.Revision)
	if err != nil || !equalAssignments(snapshot.Assignments, stored.Assignments) {
		return loader.fail(fmt.Errorf("%w: revision and assignment records disagree", ErrPolicyUnavailable))
	}
	if err := loader.engine.Apply(stored, active.ETag); err != nil {
		return loader.fail(fmt.Errorf("%w: compile active revision", ErrPolicyUnavailable))
	}
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

func decodePolicySpec(spec json.RawMessage, revision uint64) (adminauthorization.Snapshot, error) {
	decoder := json.NewDecoder(bytes.NewReader(spec))
	decoder.DisallowUnknownFields()
	var value struct {
		Version     int                             `json:"version"`
		Assignments []adminauthorization.Assignment `json:"assignments"`
	}
	if err := decoder.Decode(&value); err != nil {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: decode policy revision", ErrPolicyUnavailable)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: policy revision has trailing content", ErrPolicyUnavailable)
	}
	snapshot := adminauthorization.Snapshot{Version: value.Version, Revision: revision, Assignments: value.Assignments}
	if _, err := adminauthorization.New(snapshot); err != nil {
		return adminauthorization.Snapshot{}, fmt.Errorf("%w: validate policy revision", ErrPolicyUnavailable)
	}
	return snapshot, nil
}

func assignmentSnapshot(rows []storage.AdminAssignment, revision uint64) (adminauthorization.Snapshot, error) {
	result := adminauthorization.Snapshot{
		Version: adminauthorization.CurrentVersion, Revision: revision,
		Assignments: make([]adminauthorization.Assignment, 0, len(rows)),
	}
	for _, row := range rows {
		if row.PolicyRevision != revision {
			return adminauthorization.Snapshot{}, ErrPolicyUnavailable
		}
		assignment := adminauthorization.Assignment{ID: row.ID, Role: adminauthorization.Role(row.Role)}
		if err := json.Unmarshal(row.Subjects, &assignment.Subjects); err != nil {
			return adminauthorization.Snapshot{}, ErrPolicyUnavailable
		}
		if err := json.Unmarshal(row.Groups, &assignment.Groups); err != nil {
			return adminauthorization.Snapshot{}, ErrPolicyUnavailable
		}
		if err := json.Unmarshal(row.Namespaces, &assignment.Namespaces); err != nil {
			return adminauthorization.Snapshot{}, ErrPolicyUnavailable
		}
		result.Assignments = append(result.Assignments, assignment)
	}
	if _, err := adminauthorization.New(result); err != nil {
		return adminauthorization.Snapshot{}, ErrPolicyUnavailable
	}
	return result, nil
}

func equalAssignments(left, right []adminauthorization.Assignment) bool {
	left = canonicalAssignments(left)
	right = canonicalAssignments(right)
	return reflect.DeepEqual(left, right)
}

func canonicalAssignments(source []adminauthorization.Assignment) []adminauthorization.Assignment {
	result := make([]adminauthorization.Assignment, len(source))
	for index, assignment := range source {
		assignment.Subjects = append([]string(nil), assignment.Subjects...)
		assignment.Groups = append([]string(nil), assignment.Groups...)
		assignment.Namespaces = append([]string(nil), assignment.Namespaces...)
		sort.Strings(assignment.Subjects)
		sort.Strings(assignment.Groups)
		sort.Strings(assignment.Namespaces)
		result[index] = assignment
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func policySpecHash(spec json.RawMessage) string {
	digest := sha256.Sum256(spec)
	return hex.EncodeToString(digest[:])
}
