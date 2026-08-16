// Package authorization implements KubeLoop's unified authorization model.
// Management and Gateway requests are evaluated by the same immutable policy.
package authorization

const CurrentVersion = 2

type AuthenticationType string

const (
	AuthenticationNormal AuthenticationType = "normal"
)

type Capability string

type Subject struct {
	ID             string
	Groups         []string
	Authentication AuthenticationType
}

type GroupAccess struct {
	GroupID       string   `json:"groupId"`
	Administrator bool     `json:"administrator"`
	Namespaces    []string `json:"namespaces,omitempty"`
}

type Snapshot struct {
	Version int           `json:"version"`
	Groups  []GroupAccess `json:"groups"`
}

// Resource and Operation remain internal request-construction helpers. Public
// policy documents contain Capability IDs only.
type Resource string
type Operation string

const (
	ResourceStatus        Resource = "status"
	ResourceConfiguration Resource = "configuration"
	ResourceOAuthClient   Resource = "oauth-client"
	ResourceIdentity      Resource = "identity"
	ResourceUser          Resource = "user"
	ResourceSession       Resource = "session"
	ResourceTask          Resource = "task"
	ResourceRelay         Resource = "relay"
	ResourceAudit         Resource = "audit"
	ResourceDiagnostic    Resource = "diagnostic"
	ResourceStorage       Resource = "storage"
	ResourceUpgrade       Resource = "upgrade"
)

const (
	OperationRead    Operation = "read"
	OperationList    Operation = "list"
	OperationCreate  Operation = "create"
	OperationUpdate  Operation = "update"
	OperationDelete  Operation = "delete"
	OperationRevoke  Operation = "revoke"
	OperationDrain   Operation = "drain"
	OperationStop    Operation = "stop"
	OperationRecover Operation = "recover"
	OperationExport  Operation = "export"
)

type Request struct {
	Capability     Capability
	Resource       Resource
	Operation      Operation
	Namespace      string
	OrganizationID string
	ResourceName   string
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
		}
	}
	return capabilityForManagement(request.Resource, request.Operation)
}

type Reason string

const (
	ReasonAllowed          Reason = "allowed"
	ReasonInvalidRequest   Reason = "invalid_request"
	ReasonNoMatchingAllow  Reason = "no_matching_allow"
	ReasonExplicitDeny     Reason = "explicit_deny"
	ReasonScopeUnavailable Reason = "scope_unavailable"
)

type Match struct {
	GroupID   string `json:"groupId"`
	Namespace string `json:"namespace,omitempty"`
}

type Decision struct {
	Allowed        bool               `json:"allowed"`
	Reason         Reason             `json:"reason"`
	MatchingAllow  []Match            `json:"matchingAllow,omitempty"`
	Authentication AuthenticationType `json:"authenticationType,omitempty"`
}
