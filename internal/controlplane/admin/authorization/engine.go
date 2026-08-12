package authorization

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
)

var (
	assignmentIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	namespacePattern    = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
)

type Option func(*engineOptions) error

type engineOptions struct {
	bootstrap       BootstrapConfig
	bootstrapState  BootstrapState
	breakGlassState BreakGlassStateReader
}

func WithBootstrap(config BootstrapConfig, state BootstrapState) Option {
	return func(options *engineOptions) error {
		compiledSubjects, err := compileSubjects(config.Subjects, "bootstrap subjects")
		if err != nil {
			return err
		}
		compiledGroups, err := compileExactValues(config.Groups, "bootstrap groups")
		if err != nil {
			return err
		}
		if len(compiledSubjects) == 0 && len(compiledGroups) == 0 {
			if config.RecoveryEnabled {
				return errors.New("bootstrap recovery requires at least one subject or group")
			}
			options.bootstrap = BootstrapConfig{}
			options.bootstrapState = nil
			return nil
		}
		options.bootstrap = BootstrapConfig{
			Subjects: compiledSubjects, Groups: compiledGroups, RecoveryEnabled: config.RecoveryEnabled,
		}
		options.bootstrapState = state
		return nil
	}
}

func WithBreakGlass(state BreakGlassStateReader) Option {
	return func(options *engineOptions) error {
		options.breakGlassState = state
		return nil
	}
}

type Engine struct {
	snapshot atomic.Pointer[compiledSnapshot]
	options  engineOptions
}

type compiledSnapshot struct {
	revision    uint64
	etag        uint64
	available   bool
	assignments []compiledAssignment
}

type compiledAssignment struct {
	id         string
	role       Role
	subjects   map[string]struct{}
	groups     map[string]struct{}
	namespaces map[string]struct{}
}

func New(snapshot Snapshot, optionValues ...Option) (*Engine, error) {
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	var options engineOptions
	for _, option := range optionValues {
		if option != nil {
			if err := option(&options); err != nil {
				return nil, err
			}
		}
	}
	engine := &Engine{options: options}
	engine.snapshot.Store(compiled)
	return engine, nil
}

func NewDenyAll(optionValues ...Option) (*Engine, error) {
	return New(Snapshot{Version: CurrentVersion, Assignments: []Assignment{}}, optionValues...)
}

func (engine *Engine) Revision() uint64 {
	if engine == nil || engine.snapshot.Load() == nil {
		return 0
	}
	return engine.snapshot.Load().revision
}

func (engine *Engine) ETag() uint64 {
	if engine == nil || engine.snapshot.Load() == nil {
		return 0
	}
	return engine.snapshot.Load().etag
}

func (engine *Engine) Available() bool {
	if engine == nil || engine.snapshot.Load() == nil {
		return false
	}
	return engine.snapshot.Load().available
}

// DelegatedNamespaces returns only namespace-admin scopes matching the current
// regular identity. Callers must still authorize every concrete operation;
// this is capability discovery, not an authorization decision.
func (engine *Engine) DelegatedNamespaces(subject Subject) []string {
	if engine == nil || subject.Authentication != AuthenticationNormal {
		return nil
	}
	subject.ID = strings.TrimSpace(subject.ID)
	if _, err := uuid.Parse(subject.ID); err != nil {
		return nil
	}
	groups, err := compileExactValues(subject.Groups, "groups")
	if err != nil {
		return nil
	}
	subject.Groups = groups
	snapshot := engine.snapshot.Load()
	if snapshot == nil || !snapshot.available {
		return nil
	}
	namespaces := make(map[string]struct{})
	for _, assignment := range snapshot.assignments {
		if assignment.role != RoleNamespaceAdmin || !assignment.matchesSubject(subject) {
			continue
		}
		for namespace := range assignment.namespaces {
			namespaces[namespace] = struct{}{}
		}
	}
	result := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		result = append(result, namespace)
	}
	slices.Sort(result)
	return result
}

func (engine *Engine) Update(snapshot Snapshot) error {
	if engine == nil {
		return errors.New("management authorizer is nil")
	}
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return err
	}
	active := engine.snapshot.Load()
	if active != nil && compiled.revision <= active.revision {
		return fmt.Errorf("management policy revision %d must be newer than active revision %d", compiled.revision, active.revision)
	}
	if active != nil {
		compiled.etag = active.etag + 1
	} else {
		compiled.etag = 1
	}
	engine.snapshot.Store(compiled)
	return nil
}

// Apply installs the database-selected active snapshot. Unlike Update it may
// move to an older immutable revision during rollback; the active pointer ETag
// must always increase, preventing stale pollers from restoring newer-looking
// but no longer active content.
func (engine *Engine) Apply(snapshot Snapshot, etag uint64) error {
	if engine == nil {
		return errors.New("management authorizer is nil")
	}
	if etag == 0 {
		return errors.New("management policy ETag must be positive")
	}
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return err
	}
	active := engine.snapshot.Load()
	if active != nil && (etag < active.etag || etag == active.etag && active.available) {
		return fmt.Errorf("management policy ETag %d must be newer than active ETag %d", etag, active.etag)
	}
	compiled.etag = etag
	compiled.available = true
	engine.snapshot.Store(compiled)
	return nil
}

// FailClosed immediately removes all database-backed grants while preserving
// the last observed revision and ETag. A later Apply with that same ETag may
// restore a verified snapshot after a transient storage failure.
func (engine *Engine) FailClosed() {
	if engine == nil {
		return
	}
	active := engine.snapshot.Load()
	if active == nil || !active.available {
		return
	}
	engine.snapshot.Store(&compiledSnapshot{revision: active.revision, etag: active.etag})
}

func (engine *Engine) Authorize(ctx context.Context, subject Subject, request Request) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	if engine == nil {
		return Decision{Reason: ReasonNoMatchingAssignment}
	}
	snapshot := engine.snapshot.Load()
	revision := uint64(0)
	if snapshot != nil {
		revision = snapshot.revision
	}
	subject, request, valid := normalize(subject, request)
	if !valid {
		return Decision{Reason: ReasonInvalidRequest, Revision: revision}
	}
	if subject.Authentication == AuthenticationBreakGlass {
		return engine.authorizeBreakGlass(ctx, subject, request, revision)
	}
	if subject.Authentication != AuthenticationNormal && subject.Authentication != AuthenticationBootstrap {
		return Decision{Reason: ReasonInvalidRequest, Revision: revision}
	}
	if subject.Authentication == AuthenticationNormal && snapshot != nil {
		for _, assignment := range snapshot.assignments {
			if assignment.matches(subject, request) && roleAllows(assignment.role, request) {
				return allowedDecision(request, revision, assignment.role, assignment.id, AuthenticationNormal)
			}
		}
	}
	if !matchesBootstrap(engine.options.bootstrap, subject) {
		return Decision{Reason: ReasonNoMatchingAssignment, Revision: revision}
	}
	if engine.options.bootstrapState == nil {
		return Decision{Reason: ReasonBootstrapStateUnavailable, Revision: revision}
	}
	retired, err := engine.options.bootstrapState.BootstrapRetired(ctx)
	if err != nil {
		return Decision{Reason: ReasonBootstrapStateUnavailable, Revision: revision}
	}
	if retired && !engine.options.bootstrap.RecoveryEnabled {
		return Decision{Reason: ReasonBootstrapRetired, Revision: revision}
	}
	if !roleAllows(RolePlatformAdmin, request) {
		return Decision{Reason: ReasonNoMatchingAssignment, Revision: revision}
	}
	return allowedDecision(request, revision, RolePlatformAdmin, "", AuthenticationBootstrap)
}

// DryRun evaluates a regular identity against the current revision. It cannot
// be used to manufacture bootstrap or break-glass authentication context.
func (engine *Engine) DryRun(ctx context.Context, subject Subject, request Request) Decision {
	subject.Authentication = AuthenticationNormal
	subject.BreakGlassGeneration = ""
	return engine.Authorize(ctx, subject, request)
}

func (engine *Engine) authorizeBreakGlass(
	ctx context.Context,
	subject Subject,
	request Request,
	revision uint64,
) Decision {
	if engine.options.breakGlassState == nil {
		return Decision{Reason: ReasonBreakGlassUnavailable, Revision: revision}
	}
	state, err := engine.options.breakGlassState.CurrentBreakGlassState(ctx)
	if err != nil || !state.Enabled || state.Generation == "" {
		return Decision{Reason: ReasonBreakGlassUnavailable, Revision: revision}
	}
	if subject.BreakGlassGeneration == "" || subtle.ConstantTimeCompare(
		[]byte(subject.BreakGlassGeneration), []byte(state.Generation),
	) != 1 {
		return Decision{Reason: ReasonBreakGlassStale, Revision: revision}
	}
	if !roleAllows(RolePlatformAdmin, request) {
		return Decision{Reason: ReasonNoMatchingAssignment, Revision: revision}
	}
	return allowedDecision(request, revision, RolePlatformAdmin, "", AuthenticationBreakGlass)
}

func allowedDecision(
	request Request,
	revision uint64,
	role Role,
	assignmentID string,
	authentication AuthenticationType,
) Decision {
	scopeValue := "$cluster"
	if request.Namespace != "" {
		scopeValue = request.Namespace
	}
	return Decision{
		Allowed: true, Reason: ReasonAllowed, Role: role, AssignmentID: assignmentID,
		Revision: revision, Scope: scopeValue, Authentication: authentication,
	}
}

func (assignment compiledAssignment) matches(subject Subject, request Request) bool {
	if !assignment.matchesSubject(subject) {
		return false
	}
	if assignment.role != RoleNamespaceAdmin {
		return true
	}
	_, namespaceMatch := assignment.namespaces[request.Namespace]
	return namespaceMatch
}

func (assignment compiledAssignment) matchesSubject(subject Subject) bool {
	_, subjectMatch := assignment.subjects[subject.ID]
	groupMatch := false
	for _, group := range subject.Groups {
		if _, exists := assignment.groups[group]; exists {
			groupMatch = true
			break
		}
	}
	if !subjectMatch && !groupMatch {
		return false
	}
	return true
}

func matchesBootstrap(config BootstrapConfig, subject Subject) bool {
	if slices.Contains(config.Subjects, subject.ID) {
		return true
	}
	for _, expected := range config.Groups {
		if slices.Contains(subject.Groups, expected) {
			return true
		}
	}
	return false
}

func compileSnapshot(snapshot Snapshot) (*compiledSnapshot, error) {
	if snapshot.Version == 0 {
		snapshot.Version = CurrentVersion
	}
	if snapshot.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported management policy version %d", snapshot.Version)
	}
	if len(snapshot.Assignments) > 0 && snapshot.Revision == 0 {
		return nil, errors.New("management policy with assignments requires a positive revision")
	}
	seen := make(map[string]struct{}, len(snapshot.Assignments))
	compiled := &compiledSnapshot{
		revision: snapshot.Revision, available: true,
		assignments: make([]compiledAssignment, 0, len(snapshot.Assignments)),
	}
	for index, assignment := range snapshot.Assignments {
		assignment.ID = strings.TrimSpace(assignment.ID)
		if !assignmentIDPattern.MatchString(assignment.ID) {
			return nil, fmt.Errorf("management assignment %d has an invalid ID", index)
		}
		if _, exists := seen[assignment.ID]; exists {
			return nil, fmt.Errorf("management assignment %d has duplicate ID %q", index, assignment.ID)
		}
		seen[assignment.ID] = struct{}{}
		if _, exists := rolePermissions[assignment.Role]; !exists {
			return nil, fmt.Errorf("management assignment %q has unsupported role %q", assignment.ID, assignment.Role)
		}
		subjects, err := compileSubjects(assignment.Subjects, "subjects")
		if err != nil {
			return nil, fmt.Errorf("management assignment %q: %w", assignment.ID, err)
		}
		groups, err := compileExactValues(assignment.Groups, "groups")
		if err != nil {
			return nil, fmt.Errorf("management assignment %q: %w", assignment.ID, err)
		}
		if len(subjects) == 0 && len(groups) == 0 {
			return nil, fmt.Errorf("management assignment %q requires subjects or groups", assignment.ID)
		}
		namespaces, err := compileNamespaces(assignment.Namespaces)
		if err != nil {
			return nil, fmt.Errorf("management assignment %q: %w", assignment.ID, err)
		}
		if assignment.Role == RoleNamespaceAdmin && len(namespaces) == 0 {
			return nil, fmt.Errorf("management assignment %q namespace-admin requires namespaces", assignment.ID)
		}
		if assignment.Role != RoleNamespaceAdmin && len(namespaces) != 0 {
			return nil, fmt.Errorf("management assignment %q role %q must not declare namespace delegation", assignment.ID, assignment.Role)
		}
		compiled.assignments = append(compiled.assignments, compiledAssignment{
			id: assignment.ID, role: assignment.Role,
			subjects: stringSet(subjects), groups: stringSet(groups), namespaces: stringSet(namespaces),
		})
	}
	return compiled, nil
}

func normalize(subject Subject, request Request) (Subject, Request, bool) {
	subject.ID = strings.TrimSpace(subject.ID)
	if subject.Authentication == "" {
		subject.Authentication = AuthenticationNormal
	}
	if subject.ID == "" || len(subject.ID) > 512 {
		return Subject{}, Request{}, false
	}
	if subject.Authentication != AuthenticationBreakGlass {
		if _, err := uuid.Parse(subject.ID); err != nil {
			return Subject{}, Request{}, false
		}
	}
	groups, err := compileExactValues(subject.Groups, "groups")
	if err != nil {
		return Subject{}, Request{}, false
	}
	subject.Groups = groups
	subject.BreakGlassGeneration = strings.TrimSpace(subject.BreakGlassGeneration)
	request.Resource = Resource(strings.ToLower(strings.TrimSpace(string(request.Resource))))
	request.Operation = Operation(strings.ToLower(strings.TrimSpace(string(request.Operation))))
	request.Namespace = strings.TrimSpace(request.Namespace)
	request.ResourceName = strings.TrimSpace(request.ResourceName)
	allowedScopes, resourceExists := resourceScopes[request.Resource]
	if !resourceExists || rolePermissions[RolePlatformAdmin][request.Resource][request.Operation] == 0 || len(request.ResourceName) > 512 {
		return Subject{}, Request{}, false
	}
	requestedScope := scopeCluster
	if request.Namespace != "" {
		if !namespacePattern.MatchString(request.Namespace) {
			return Subject{}, Request{}, false
		}
		requestedScope = scopeNamespace
	}
	if allowedScopes&requestedScope == 0 {
		return Subject{}, Request{}, false
	}
	return subject, request, true
}

func compileExactValues(values []string, field string) ([]string, error) {
	if len(values) > 512 {
		return nil, fmt.Errorf("%s exceeds 512 entries", field)
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 512 || value == "*" || value == "$cluster" {
			return nil, fmt.Errorf("%s contains an invalid exact value", field)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("%s contains duplicate value %q", field, value)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result, nil
}

func compileSubjects(values []string, field string) ([]string, error) {
	result, err := compileExactValues(values, field)
	if err != nil {
		return nil, err
	}
	for _, value := range result {
		if _, err := uuid.Parse(value); err != nil {
			return nil, fmt.Errorf("%s contains a non-Principal UUID", field)
		}
	}
	return result, nil
}

func compileNamespaces(values []string) ([]string, error) {
	result, err := compileExactValues(values, "namespaces")
	if err != nil {
		return nil, err
	}
	for _, namespace := range result {
		if !namespacePattern.MatchString(namespace) {
			return nil, fmt.Errorf("namespaces contains invalid namespace %q", namespace)
		}
	}
	return result, nil
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
