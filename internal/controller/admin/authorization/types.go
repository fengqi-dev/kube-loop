// Package authorization implements the Controller Management Plane RBAC
// contract. It is intentionally separate from the ordinary Gateway policy
// engine in internal/controller/authorization.
package authorization

import (
	"context"
	"errors"
	"strings"
)

const CurrentVersion = 1

type Role string

const (
	RolePlatformAdmin  Role = "platform-admin"
	RoleSecurityAdmin  Role = "security-admin"
	RoleOperator       Role = "operator"
	RoleAuditor        Role = "auditor"
	RoleNamespaceAdmin Role = "namespace-admin"
)

type AuthenticationType string

const (
	AuthenticationNormal     AuthenticationType = "normal"
	AuthenticationBootstrap  AuthenticationType = "bootstrap"
	AuthenticationBreakGlass AuthenticationType = "break-glass"
)

type Resource string

const (
	ResourceStatus          Resource = "status"
	ResourceConfiguration   Resource = "configuration"
	ResourceProvider        Resource = "provider"
	ResourceAssignment      Resource = "assignment"
	ResourcePolicy          Resource = "policy"
	ResourceIdentityMapping Resource = "identity-mapping"
	ResourcePrincipal       Resource = "principal"
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

type Operation string

const (
	OperationRead     Operation = "read"
	OperationList     Operation = "list"
	OperationCreate   Operation = "create"
	OperationUpdate   Operation = "update"
	OperationDelete   Operation = "delete"
	OperationValidate Operation = "validate"
	OperationDryRun   Operation = "dry-run"
	OperationPublish  Operation = "publish"
	OperationRollback Operation = "rollback"
	OperationRevoke   Operation = "revoke"
	OperationDrain    Operation = "drain"
	OperationStop     Operation = "stop"
	OperationRecover  Operation = "recover"
	OperationExport   Operation = "export"
)

type Subject struct {
	ID                   string
	Groups               []string
	Authentication       AuthenticationType
	BreakGlassGeneration string
}

type Request struct {
	Resource     Resource
	Operation    Operation
	Namespace    string
	ResourceName string
}

func (request Request) Key() string {
	return "admin." + string(request.Resource) + "/" + string(request.Operation)
}

type Reason string

const (
	ReasonAllowed                   Reason = "allowed"
	ReasonInvalidRequest            Reason = "invalid-request"
	ReasonNoMatchingAssignment      Reason = "no-matching-assignment"
	ReasonBootstrapStateUnavailable Reason = "bootstrap-state-unavailable"
	ReasonBootstrapRetired          Reason = "bootstrap-retired"
	ReasonBreakGlassUnavailable     Reason = "break-glass-unavailable"
	ReasonBreakGlassStale           Reason = "break-glass-stale"
)

type Decision struct {
	Allowed        bool
	Reason         Reason
	Role           Role
	AssignmentID   string
	Revision       uint64
	Scope          string
	Authentication AuthenticationType
}

type Assignment struct {
	ID         string   `json:"id"`
	Role       Role     `json:"role"`
	Subjects   []string `json:"subjects,omitempty"`
	Groups     []string `json:"groups,omitempty"`
	Namespaces []string `json:"namespaces,omitempty"`
}

type Snapshot struct {
	Version     int          `json:"version"`
	Revision    uint64       `json:"revision"`
	Assignments []Assignment `json:"assignments"`
}

type BootstrapConfig struct {
	Subjects        []string
	Groups          []string
	RecoveryEnabled bool
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

var ErrForbidden = errors.New("management operation is not permitted")

// LookupAuthorized ensures an object repository is never consulted before the
// caller's management scope is authorized. The callback receives the already
// authorized namespace and stable resource name so implementations can issue a
// scope-constrained query instead of fetching globally and filtering later.
func LookupAuthorized[T any](
	ctx context.Context,
	engine *Engine,
	subject Subject,
	request Request,
	lookup func(context.Context, string, string) (T, error),
) (T, Decision, error) {
	var zero T
	decision := engine.Authorize(ctx, subject, request)
	if !decision.Allowed {
		return zero, decision, ErrForbidden
	}
	if lookup == nil {
		return zero, decision, errors.New("management object lookup is unavailable")
	}
	value, err := lookup(ctx, strings.TrimSpace(request.Namespace), strings.TrimSpace(request.ResourceName))
	return value, decision, err
}
