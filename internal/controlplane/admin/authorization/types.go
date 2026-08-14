// Package authorization implements KubeLoop's unified authorization model.
// Management and Gateway requests are evaluated by the same immutable policy.
package authorization

import (
	"context"
	"errors"
	"strings"
)

const CurrentVersion = 2

type AuthenticationType string

const (
	AuthenticationNormal     AuthenticationType = "normal"
	AuthenticationBootstrap  AuthenticationType = "bootstrap"
	AuthenticationBreakGlass AuthenticationType = "break-glass"
)

type Effect string

const (
	EffectAllow Effect = "allow"
	EffectDeny  Effect = "deny"
)

type Capability string

type Role string

const (
	RolePlatformAdmin     Role = "platform-admin"
	RoleSecurityAdmin     Role = "security-admin"
	RoleOperator          Role = "operator"
	RoleAuditor           Role = "auditor"
	RoleNamespaceAdmin    Role = "namespace-admin"
	RoleNamespaceOperator Role = "namespace-operator"
	RoleNamespaceViewer   Role = "namespace-viewer"
)

type SubjectType string

const (
	SubjectPrincipal SubjectType = "principal"
	SubjectGroup     SubjectType = "group"
)

type ScopeType string

const (
	ScopePlatform   ScopeType = "platform"
	ScopeNamespaces ScopeType = "namespaces"
)

type ManagedBy string

const (
	ManagedByPlatform  ManagedBy = "platform"
	ManagedByDelegated ManagedBy = "delegated"
)

type Subject struct {
	ID                   string
	Provider             string
	Groups               []string
	Authentication       AuthenticationType
	BreakGlassGeneration string
}

type SubjectRef struct {
	Type        SubjectType `json:"type"`
	PrincipalID string      `json:"principalId,omitempty"`
	ProviderID  string      `json:"providerId,omitempty"`
	GroupName   string      `json:"groupName,omitempty"`
}

type NamespaceSelector struct {
	MatchLabels      map[string]string          `json:"matchLabels,omitempty"`
	MatchExpressions []LabelSelectorRequirement `json:"matchExpressions,omitempty"`
}

type LabelSelectorRequirement struct {
	Key      string   `json:"key"`
	Operator string   `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

type BindingScope struct {
	Type           ScopeType           `json:"type"`
	Names          []string            `json:"names,omitempty"`
	LabelSelectors []NamespaceSelector `json:"labelSelectors,omitempty"`
}

type Statement struct {
	Effect       Effect       `json:"effect"`
	Capabilities []Capability `json:"capabilities"`
}

type RoleDefinition struct {
	ID          Role        `json:"id"`
	DisplayName string      `json:"displayName"`
	Description string      `json:"description,omitempty"`
	Delegatable bool        `json:"delegatable,omitempty"`
	BuiltIn     bool        `json:"builtIn,omitempty"`
	Statements  []Statement `json:"statements"`
}

type Binding struct {
	ID        string       `json:"id"`
	Subject   SubjectRef   `json:"subject"`
	RoleID    Role         `json:"roleId"`
	Scope     BindingScope `json:"scope"`
	ManagedBy ManagedBy    `json:"managedBy"`
	CreatedBy string       `json:"createdBy,omitempty"`
}

type Snapshot struct {
	Version  int              `json:"version"`
	Roles    []RoleDefinition `json:"roles,omitempty"`
	Bindings []Binding        `json:"bindings"`
}

// Resource and Operation remain internal request-construction helpers. Public
// policy documents contain Capability IDs only.
type Resource string
type Operation string

const (
	ResourceStatus          Resource = "status"
	ResourceConfiguration   Resource = "configuration"
	ResourceProvider        Resource = "provider"
	ResourceOAuthClient     Resource = "oauth-client"
	ResourceAssignment      Resource = "assignment"
	ResourcePolicy          Resource = "policy"
	ResourceIdentityMapping Resource = "identity-mapping"
	ResourcePrincipal       Resource = "principal"
	ResourceUser            Resource = "user"
	ResourceSession         Resource = "session"
	ResourceTask            Resource = "task"
	ResourceRelay           Resource = "relay"
	ResourceAudit           Resource = "audit"
	ResourceDiagnostic      Resource = "diagnostic"
	ResourceStorage         Resource = "storage"
	ResourceUpgrade         Resource = "upgrade"
	ResourceNamespaceMember Resource = "namespace-member"
	ResourceNamespacePolicy Resource = "namespace-policy"
)

const (
	OperationRead     Operation = "read"
	OperationList     Operation = "list"
	OperationCreate   Operation = "create"
	OperationUpdate   Operation = "update"
	OperationDelete   Operation = "delete"
	OperationValidate Operation = "validate"
	OperationDryRun   Operation = "dry-run"
	OperationPublish  Operation = "publish"
	OperationRevoke   Operation = "revoke"
	OperationDrain    Operation = "drain"
	OperationStop     Operation = "stop"
	OperationRecover  Operation = "recover"
	OperationExport   Operation = "export"
)

type Request struct {
	Capability      Capability
	Resource        Resource
	Operation       Operation
	Namespace       string
	ResourceName    string
	NamespaceLabels map[string]string
	LabelsAvailable bool
}

func (request Request) Key() string {
	if request.Capability != "" {
		return string(request.Capability)
	}
	if request.Namespace != "" {
		switch request.Resource {
		case ResourceSession, ResourceTask:
			if request.Operation == OperationStop || request.Operation == OperationRevoke || request.Operation == OperationDelete {
				return "namespace.tasks.stop"
			}
			return "namespace.tasks.read"
		case ResourceNamespaceMember, ResourceNamespacePolicy:
			if request.Operation == OperationRead || request.Operation == OperationList {
				return "namespace.authorization.read"
			}
			return "namespace.authorization.delegate"
		}
	}
	return capabilityForManagement(request.Resource, request.Operation)
}

type Reason string

const (
	ReasonAllowed                   Reason = "allowed"
	ReasonInvalidRequest            Reason = "invalid_request"
	ReasonNoMatchingAllow           Reason = "no_matching_allow"
	ReasonExplicitDeny              Reason = "explicit_deny"
	ReasonScopeUnavailable          Reason = "scope_unavailable"
	ReasonBootstrapStateUnavailable Reason = "bootstrap_state_unavailable"
	ReasonBootstrapRetired          Reason = "bootstrap_retired"
	ReasonBreakGlassUnavailable     Reason = "break_glass_unavailable"
	ReasonBreakGlassStale           Reason = "break_glass_stale"
)

type Match struct {
	BindingID  string     `json:"bindingId"`
	RoleID     Role       `json:"roleId"`
	Effect     Effect     `json:"effect"`
	Capability Capability `json:"capability"`
	Scope      ScopeType  `json:"scope"`
}

type Decision struct {
	Allowed        bool               `json:"allowed"`
	Reason         Reason             `json:"reason"`
	MatchingAllow  []Match            `json:"matchingAllow,omitempty"`
	MatchingDeny   []Match            `json:"matchingDeny,omitempty"`
	Authentication AuthenticationType `json:"authenticationType,omitempty"`
}

type BootstrapConfig struct {
	Subjects, Groups []string
	RecoveryEnabled  bool
}
type BootstrapState interface {
	BootstrapRetired(context.Context) (bool, error)
}
type BreakGlassState struct {
	Enabled    bool
	Generation string
}
type BreakGlassStateReader interface {
	CurrentBreakGlassState(context.Context) (BreakGlassState, error)
}

var ErrForbidden = errors.New("operation is not permitted")

func LookupAuthorized[T any](ctx context.Context, engine *Engine, subject Subject, request Request,
	lookup func(context.Context, string, string) (T, error)) (T, Decision, error) {
	var zero T
	decision := engine.Authorize(ctx, subject, request)
	if !decision.Allowed {
		return zero, decision, ErrForbidden
	}
	if lookup == nil {
		return zero, decision, errors.New("authorized object lookup is unavailable")
	}
	value, err := lookup(ctx, strings.TrimSpace(request.Namespace), strings.TrimSpace(request.ResourceName))
	return value, decision, err
}
