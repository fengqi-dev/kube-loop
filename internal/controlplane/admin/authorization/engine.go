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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

var (
	bindingIDPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	roleIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9-]{2,63}$`)
	namespacePattern  = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)
	providerIDPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9._-]{0,126}[A-Za-z0-9])?$`)
)

type Option func(*engineOptions) error
type engineOptions struct {
	bootstrap       BootstrapConfig
	bootstrapState  BootstrapState
	breakGlassState BreakGlassStateReader
}

func WithBootstrap(config BootstrapConfig, state BootstrapState) Option {
	return func(options *engineOptions) error {
		subjects, err := compileSubjects(config.Subjects, "bootstrap subjects")
		if err != nil {
			return err
		}
		groups, err := compileExactValues(config.Groups, "bootstrap groups")
		if err != nil {
			return err
		}
		if len(subjects) == 0 && len(groups) == 0 {
			if config.RecoveryEnabled {
				return errors.New("bootstrap recovery requires an identity")
			}
			return nil
		}
		options.bootstrap, options.bootstrapState = BootstrapConfig{Subjects: subjects, Groups: groups, RecoveryEnabled: config.RecoveryEnabled}, state
		return nil
	}
}
func WithBreakGlass(state BreakGlassStateReader) Option {
	return func(options *engineOptions) error { options.breakGlassState = state; return nil }
}

type Engine struct {
	snapshot atomic.Pointer[compiledSnapshot]
	options  engineOptions
}
type compiledSnapshot struct {
	revision, etag uint64
	available      bool
	roles          map[Role]compiledRole
	bindings       []compiledBinding
}
type compiledRole struct {
	definition RoleDefinition
	effects    map[Capability]Effect
}
type compiledBinding struct {
	definition     Binding
	namespaceNames map[string]struct{}
	selectors      []labels.Selector
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
func NewDenyAll(options ...Option) (*Engine, error) {
	return New(Snapshot{Version: CurrentVersion, Bindings: []Binding{}}, options...)
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
	return engine != nil && engine.snapshot.Load() != nil && engine.snapshot.Load().available
}

func (engine *Engine) Apply(snapshot Snapshot, etag uint64) error {
	if engine == nil || etag == 0 {
		return errors.New("authorization engine and positive ETag are required")
	}
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return err
	}
	active := engine.snapshot.Load()
	if active != nil && (etag < active.etag || etag == active.etag && active.available) {
		return fmt.Errorf("authorization ETag %d is not newer than %d", etag, active.etag)
	}
	compiled.etag, compiled.available = etag, true
	engine.snapshot.Store(compiled)
	return nil
}
func (engine *Engine) Update(snapshot Snapshot) error {
	if engine == nil {
		return errors.New("authorization engine is nil")
	}
	compiled, err := compileSnapshot(snapshot)
	if err != nil {
		return err
	}
	active := engine.snapshot.Load()
	if active != nil && compiled.revision <= active.revision {
		return errors.New("authorization revision must increase")
	}
	if active == nil {
		compiled.etag = 1
	} else {
		compiled.etag = active.etag + 1
	}
	engine.snapshot.Store(compiled)
	return nil
}
func (engine *Engine) FailClosed() {
	if engine == nil {
		return
	}
	active := engine.snapshot.Load()
	if active != nil && active.available {
		engine.snapshot.Store(&compiledSnapshot{revision: active.revision, etag: active.etag})
	}
}

func (engine *Engine) Authorize(ctx context.Context, subject Subject, request Request) Decision {
	if ctx == nil {
		ctx = context.Background()
	}
	snapshot := engineSnapshot(engine)
	revision := snapshotRevision(snapshot)
	subject, request, valid := normalize(subject, request)
	if !valid {
		return Decision{Reason: ReasonInvalidRequest, Revision: revision}
	}
	if request.Namespace != "" && !request.LabelsAvailable && labelsRequiredForDecision(snapshot, subject, request) {
		return Decision{Reason: ReasonScopeUnavailable, Revision: revision}
	}
	if subject.Authentication == AuthenticationBreakGlass {
		return engine.authorizeBreakGlass(ctx, subject, revision)
	}
	if subject.Authentication != AuthenticationNormal && subject.Authentication != AuthenticationBootstrap {
		return Decision{Reason: ReasonInvalidRequest, Revision: revision}
	}
	decision := evaluate(snapshot, subject, request)
	if decision.Allowed || decision.Reason == ReasonExplicitDeny {
		return decision
	}
	return engine.authorizeBootstrap(ctx, subject, request, decision)
}

// UsesNamespaceSelectors reports whether the active immutable snapshot needs
// Kubernetes Namespace labels to make at least one authorization decision.
func (engine *Engine) UsesNamespaceSelectors() bool {
	return snapshotUsesSelectors(engineSnapshot(engine))
}

func evaluate(snapshot *compiledSnapshot, subject Subject, request Request) Decision {
	decision := Decision{Reason: ReasonNoMatchingAllow, Revision: snapshotRevision(snapshot), Authentication: AuthenticationNormal}
	if snapshot == nil || !snapshot.available {
		return decision
	}
	required := []Capability{request.Capability}
	if request.Namespace != "" && request.Capability != CapabilityNamespaceAccess {
		required = append([]Capability{CapabilityNamespaceAccess}, required...)
	}
	for _, capability := range required {
		allows := []Match{}
		denies := []Match{}
		for _, binding := range snapshot.bindings {
			if !binding.matchesSubject(subject) || !binding.matchesScope(request) {
				continue
			}
			role := snapshot.roles[binding.definition.RoleID]
			effect, exists := role.effects[capability]
			if !exists {
				continue
			}
			match := Match{BindingID: binding.definition.ID, RoleID: binding.definition.RoleID, Effect: effect, Capability: capability, Scope: binding.definition.Scope.Type}
			if effect == EffectDeny {
				denies = append(denies, match)
			} else {
				allows = append(allows, match)
			}
		}
		decision.MatchingAllow = append(decision.MatchingAllow, allows...)
		decision.MatchingDeny = append(decision.MatchingDeny, denies...)
		if len(denies) > 0 {
			decision.Reason = ReasonExplicitDeny
			return decision
		}
		if len(allows) == 0 {
			return decision
		}
	}
	decision.Allowed, decision.Reason = true, ReasonAllowed
	return decision
}

func (engine *Engine) DryRun(ctx context.Context, subject Subject, request Request) Decision {
	subject.Authentication, subject.BreakGlassGeneration = AuthenticationNormal, ""
	return engine.Authorize(ctx, subject, request)
}
func (engine *Engine) authorizeBootstrap(ctx context.Context, subject Subject, request Request, denied Decision) Decision {
	if engine == nil || !matchesBootstrap(engine.options.bootstrap, subject) {
		return denied
	}
	if engine.options.bootstrapState == nil {
		denied.Reason = ReasonBootstrapStateUnavailable
		return denied
	}
	retired, err := engine.options.bootstrapState.BootstrapRetired(ctx)
	if err != nil {
		denied.Reason = ReasonBootstrapStateUnavailable
		return denied
	}
	if retired && !engine.options.bootstrap.RecoveryEnabled {
		denied.Reason = ReasonBootstrapRetired
		return denied
	}
	if !strings.HasPrefix(string(request.Capability), "platform.") {
		return denied
	}
	return Decision{Allowed: true, Reason: ReasonAllowed, Revision: denied.Revision, Authentication: AuthenticationBootstrap}
}
func (engine *Engine) authorizeBreakGlass(ctx context.Context, subject Subject, revision uint64) Decision {
	if engine == nil || engine.options.breakGlassState == nil {
		return Decision{Reason: ReasonBreakGlassUnavailable, Revision: revision}
	}
	state, err := engine.options.breakGlassState.CurrentBreakGlassState(ctx)
	if err != nil || !state.Enabled || state.Generation == "" {
		return Decision{Reason: ReasonBreakGlassUnavailable, Revision: revision}
	}
	if subject.BreakGlassGeneration == "" || subtle.ConstantTimeCompare([]byte(subject.BreakGlassGeneration), []byte(state.Generation)) != 1 {
		return Decision{Reason: ReasonBreakGlassStale, Revision: revision}
	}
	return Decision{Allowed: true, Reason: ReasonAllowed, Revision: revision, Authentication: AuthenticationBreakGlass}
}

func (engine *Engine) DelegatedNamespaces(subject Subject) []string {
	snapshot := engineSnapshot(engine)
	if snapshot == nil {
		return nil
	}
	result := map[string]struct{}{}
	for _, binding := range snapshot.bindings {
		if binding.matchesSubject(subject) {
			for name := range binding.namespaceNames {
				result[name] = struct{}{}
			}
		}
	}
	values := make([]string, 0, len(result))
	for value := range result {
		values = append(values, value)
	}
	slices.Sort(values)
	return values
}

func compileSnapshot(snapshot Snapshot) (*compiledSnapshot, error) {
	if snapshot.Version == 0 {
		snapshot.Version = CurrentVersion
	}
	if snapshot.Version != CurrentVersion {
		return nil, fmt.Errorf("unsupported authorization policy version %d", snapshot.Version)
	}
	if len(snapshot.Bindings) > 0 && snapshot.Revision == 0 {
		return nil, errors.New("authorization bindings require a revision")
	}
	compiled := &compiledSnapshot{revision: snapshot.Revision, available: true, roles: map[Role]compiledRole{}, bindings: make([]compiledBinding, 0, len(snapshot.Bindings))}
	for _, definition := range append(BuiltInRoleDefinitions(), snapshot.Roles...) {
		role, err := compileRole(definition, definition.BuiltIn)
		if err != nil {
			return nil, err
		}
		if _, exists := compiled.roles[definition.ID]; exists {
			return nil, fmt.Errorf("duplicate role %q", definition.ID)
		}
		compiled.roles[definition.ID] = role
	}
	seen := map[string]struct{}{}
	for _, definition := range snapshot.Bindings {
		binding, err := compileBinding(definition)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[definition.ID]; exists {
			return nil, fmt.Errorf("duplicate binding %q", definition.ID)
		}
		seen[definition.ID] = struct{}{}
		role, exists := compiled.roles[definition.RoleID]
		if !exists {
			return nil, fmt.Errorf("binding %q references unknown role", definition.ID)
		}
		if definition.ManagedBy == ManagedByDelegated && !validDelegatableRole(role.definition) {
			return nil, fmt.Errorf("binding %q references a non-delegatable role", definition.ID)
		}
		if roleHasPlatformCapability(role) && definition.Scope.Type != ScopePlatform {
			return nil, fmt.Errorf("binding %q applies a platform role outside platform scope", definition.ID)
		}
		compiled.bindings = append(compiled.bindings, binding)
	}
	return compiled, nil
}
func compileRole(definition RoleDefinition, builtIn bool) (compiledRole, error) {
	definition.ID, definition.DisplayName, definition.Description = Role(strings.TrimSpace(string(definition.ID))), strings.TrimSpace(definition.DisplayName), strings.TrimSpace(definition.Description)
	if !roleIDPattern.MatchString(string(definition.ID)) || definition.DisplayName == "" || len(definition.DisplayName) > 128 || len(definition.Description) > 512 {
		return compiledRole{}, errors.New("authorization role is invalid")
	}
	if !builtIn {
		for _, role := range BuiltInRoleDefinitions() {
			if role.ID == definition.ID {
				return compiledRole{}, errors.New("custom role conflicts with built-in role")
			}
		}
	}
	if len(definition.Statements) == 0 || len(definition.Statements) > 64 {
		return compiledRole{}, errors.New("authorization role requires statements")
	}
	effects := map[Capability]Effect{}
	for _, statement := range definition.Statements {
		if statement.Effect != EffectAllow && statement.Effect != EffectDeny || len(statement.Capabilities) == 0 {
			return compiledRole{}, errors.New("authorization statement is invalid")
		}
		for _, capability := range statement.Capabilities {
			if _, valid := capabilitySet[capability]; !valid {
				return compiledRole{}, fmt.Errorf("unsupported capability %q", capability)
			}
			if previous, duplicate := effects[capability]; duplicate && previous != statement.Effect {
				return compiledRole{}, fmt.Errorf("role %q both allows and denies %q", definition.ID, capability)
			}
			effects[capability] = statement.Effect
		}
	}
	return compiledRole{definition: definition, effects: effects}, nil
}
func compileBinding(definition Binding) (compiledBinding, error) {
	definition.ID, definition.CreatedBy = strings.TrimSpace(definition.ID), strings.TrimSpace(definition.CreatedBy)
	if !bindingIDPattern.MatchString(definition.ID) {
		return compiledBinding{}, errors.New("authorization binding ID is invalid")
	}
	if definition.ManagedBy == "" {
		definition.ManagedBy = ManagedByPlatform
	}
	if definition.ManagedBy != ManagedByPlatform && definition.ManagedBy != ManagedByDelegated {
		return compiledBinding{}, errors.New("authorization binding owner is invalid")
	}
	if err := validateSubjectRef(&definition.Subject); err != nil {
		return compiledBinding{}, err
	}
	compiled := compiledBinding{definition: definition, namespaceNames: map[string]struct{}{}}
	switch definition.Scope.Type {
	case ScopePlatform:
		if len(definition.Scope.Names) != 0 || len(definition.Scope.LabelSelectors) != 0 {
			return compiledBinding{}, errors.New("platform scope cannot contain namespaces")
		}
	case ScopeNamespaces:
		if len(definition.Scope.Names) == 0 && len(definition.Scope.LabelSelectors) == 0 {
			return compiledBinding{}, errors.New("namespace scope cannot be empty")
		}
		for _, name := range definition.Scope.Names {
			name = strings.TrimSpace(name)
			if !namespacePattern.MatchString(name) {
				return compiledBinding{}, errors.New("namespace scope contains an invalid name")
			}
			if _, duplicate := compiled.namespaceNames[name]; duplicate {
				return compiledBinding{}, errors.New("namespace scope contains duplicate names")
			}
			compiled.namespaceNames[name] = struct{}{}
		}
		if definition.ManagedBy == ManagedByDelegated && len(definition.Scope.LabelSelectors) != 0 {
			return compiledBinding{}, errors.New("delegated bindings cannot use label selectors")
		}
		for _, selector := range definition.Scope.LabelSelectors {
			value, err := compileLabelSelector(selector)
			if err != nil {
				return compiledBinding{}, err
			}
			compiled.selectors = append(compiled.selectors, value)
		}
	default:
		return compiledBinding{}, errors.New("authorization binding scope is invalid")
	}
	return compiled, nil
}
func validateSubjectRef(subject *SubjectRef) error {
	subject.PrincipalID, subject.ProviderID, subject.GroupName = strings.TrimSpace(subject.PrincipalID), strings.TrimSpace(subject.ProviderID), strings.TrimSpace(subject.GroupName)
	switch subject.Type {
	case SubjectPrincipal:
		if uuid.Validate(subject.PrincipalID) != nil || subject.ProviderID != "" || subject.GroupName != "" {
			return errors.New("principal binding subject is invalid")
		}
	case SubjectGroup:
		if subject.PrincipalID != "" || !providerIDPattern.MatchString(subject.ProviderID) || subject.GroupName == "" || len(subject.GroupName) > 512 || subject.GroupName == "*" {
			return errors.New("group binding subject is invalid")
		}
	default:
		return errors.New("authorization binding subject type is invalid")
	}
	return nil
}
func compileLabelSelector(selector NamespaceSelector) (labels.Selector, error) {
	if len(selector.MatchLabels) == 0 && len(selector.MatchExpressions) == 0 {
		return nil, errors.New("empty namespace label selector is forbidden")
	}
	value := &metav1.LabelSelector{MatchLabels: selector.MatchLabels}
	for _, expression := range selector.MatchExpressions {
		value.MatchExpressions = append(value.MatchExpressions, metav1.LabelSelectorRequirement{Key: expression.Key, Operator: metav1.LabelSelectorOperator(expression.Operator), Values: expression.Values})
	}
	compiled, err := metav1.LabelSelectorAsSelector(value)
	if err != nil {
		return nil, fmt.Errorf("compile namespace label selector: %w", err)
	}
	return compiled, nil
}
func (binding compiledBinding) matchesSubject(subject Subject) bool {
	if binding.definition.Subject.Type == SubjectPrincipal {
		return binding.definition.Subject.PrincipalID == subject.ID
	}
	return binding.definition.Subject.ProviderID == subject.Provider && slices.Contains(subject.Groups, binding.definition.Subject.GroupName)
}
func (binding compiledBinding) matchesScope(request Request) bool {
	if binding.definition.Scope.Type == ScopePlatform {
		return request.Namespace == "" || strings.HasPrefix(string(request.Capability), "namespace.")
	}
	if request.Namespace == "" {
		return false
	}
	if _, exists := binding.namespaceNames[request.Namespace]; exists {
		return true
	}
	if !request.LabelsAvailable {
		return false
	}
	set := labels.Set(request.NamespaceLabels)
	for _, selector := range binding.selectors {
		if selector.Matches(set) {
			return true
		}
	}
	return false
}
func normalize(subject Subject, request Request) (Subject, Request, bool) {
	subject.ID, subject.Provider, subject.BreakGlassGeneration = strings.TrimSpace(subject.ID), strings.TrimSpace(subject.Provider), strings.TrimSpace(subject.BreakGlassGeneration)
	if subject.Authentication == "" {
		subject.Authentication = AuthenticationNormal
	}
	if subject.Authentication != AuthenticationBreakGlass && uuid.Validate(subject.ID) != nil {
		return Subject{}, Request{}, false
	}
	groups, err := compileExactValues(subject.Groups, "groups")
	if err != nil {
		return Subject{}, Request{}, false
	}
	subject.Groups = groups
	request.Capability = Capability(strings.TrimSpace(request.Key()))
	if _, exists := capabilitySet[request.Capability]; !exists {
		return Subject{}, Request{}, false
	}
	request.Namespace, request.ResourceName = strings.TrimSpace(request.Namespace), strings.TrimSpace(request.ResourceName)
	if request.Namespace != "" && !namespacePattern.MatchString(request.Namespace) || len(request.ResourceName) > 512 {
		return Subject{}, Request{}, false
	}
	if strings.HasPrefix(string(request.Capability), "namespace.") != (request.Namespace != "") {
		return Subject{}, Request{}, false
	}
	return subject, request, true
}
func compileSubjects(values []string, field string) ([]string, error) {
	result, err := compileExactValues(values, field)
	if err != nil {
		return nil, err
	}
	for _, value := range result {
		if uuid.Validate(value) != nil {
			return nil, fmt.Errorf("%s contains an invalid principal", field)
		}
	}
	return result, nil
}
func compileExactValues(values []string, field string) ([]string, error) {
	if len(values) > 512 {
		return nil, fmt.Errorf("%s exceeds 512 entries", field)
	}
	result := []string{}
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 512 || value == "*" || value == "$cluster" {
			return nil, fmt.Errorf("%s contains an invalid value", field)
		}
		if _, duplicate := seen[value]; duplicate {
			return nil, fmt.Errorf("%s contains duplicate values", field)
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}
func matchesBootstrap(config BootstrapConfig, subject Subject) bool {
	return slices.Contains(config.Subjects, subject.ID) || slices.ContainsFunc(config.Groups, func(group string) bool { return slices.Contains(subject.Groups, group) })
}
func validDelegatableRole(role RoleDefinition) bool {
	if !role.Delegatable {
		return false
	}
	for _, statement := range role.Statements {
		if statement.Effect != EffectAllow {
			return false
		}
		for _, capability := range statement.Capabilities {
			if !strings.HasPrefix(string(capability), "namespace.") || capability == "namespace.authorization.delegate" {
				return false
			}
		}
	}
	return true
}
func roleHasPlatformCapability(role compiledRole) bool {
	for capability := range role.effects {
		if strings.HasPrefix(string(capability), "platform.") {
			return true
		}
	}
	return false
}
func engineSnapshot(engine *Engine) *compiledSnapshot {
	if engine == nil {
		return nil
	}
	return engine.snapshot.Load()
}
func snapshotRevision(snapshot *compiledSnapshot) uint64 {
	if snapshot == nil {
		return 0
	}
	return snapshot.revision
}
func snapshotUsesSelectors(snapshot *compiledSnapshot) bool {
	if snapshot == nil {
		return false
	}
	for _, binding := range snapshot.bindings {
		if len(binding.selectors) > 0 {
			return true
		}
	}
	return false
}

func labelsRequiredForDecision(snapshot *compiledSnapshot, subject Subject, request Request) bool {
	if snapshot == nil {
		return false
	}
	required := []Capability{request.Capability}
	if request.Capability != CapabilityNamespaceAccess {
		required = append(required, CapabilityNamespaceAccess)
	}
	for _, binding := range snapshot.bindings {
		if len(binding.selectors) == 0 || !binding.matchesSubject(subject) || binding.definition.Scope.Type != ScopeNamespaces {
			continue
		}
		if _, exact := binding.namespaceNames[request.Namespace]; exact {
			continue
		}
		role := snapshot.roles[binding.definition.RoleID]
		for _, capability := range required {
			if _, relevant := role.effects[capability]; relevant {
				return true
			}
		}
	}
	return false
}
