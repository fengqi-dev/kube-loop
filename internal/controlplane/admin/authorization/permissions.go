package authorization

import (
	"slices"
	"strings"
)

const (
	CapabilityNamespaceAccess Capability = "namespace.access"
)

var capabilityCatalog = []Capability{
	"platform.overview.read", "platform.configuration.read", "platform.configuration.manage",
	"platform.identity.providers.read", "platform.identity.providers.manage", "platform.identity.principals.read",
	"platform.identity.users.read", "platform.identity.users.manage", "platform.oauth-clients.read", "platform.oauth-clients.manage",
	"platform.authorization.read", "platform.authorization.manage", "platform.authorization.publish",
	"platform.authorization.rollback", "platform.authorization.simulate", "platform.sessions.read", "platform.sessions.revoke",
	"platform.tasks.read", "platform.tasks.stop", "platform.relays.read", "platform.relays.manage",
	"platform.audit.read", "platform.audit.export", "platform.diagnostics.read",
	CapabilityNamespaceAccess, "namespace.resources.read", "namespace.exec.open", "namespace.port-forward.open",
	"namespace.files.download", "namespace.files.upload", "namespace.intercepts.read", "namespace.intercepts.manage",
	"namespace.previews.read", "namespace.previews.manage", "namespace.tasks.read", "namespace.tasks.stop",
	"namespace.authorization.read", "namespace.authorization.delegate",
}

var capabilitySet = func() map[Capability]struct{} {
	result := make(map[Capability]struct{}, len(capabilityCatalog))
	for _, capability := range capabilityCatalog {
		result[capability] = struct{}{}
	}
	return result
}()

func AvailableCapabilities() []Capability { return append([]Capability(nil), capabilityCatalog...) }
func AvailablePermissions() []string {
	result := make([]string, len(capabilityCatalog))
	for i, value := range capabilityCatalog {
		result[i] = string(value)
	}
	return result
}

func allow(capabilities ...Capability) Statement {
	return Statement{Effect: EffectAllow, Capabilities: capabilities}
}

func BuiltInRoleDefinitions() []RoleDefinition {
	platform := append([]Capability(nil), capabilityCatalog...)
	security := capabilitiesWithPrefixes("platform.overview.", "platform.identity.", "platform.oauth-clients.", "platform.sessions.", "platform.audit.")
	security = append(security, "platform.authorization.read", "platform.authorization.simulate")
	operator := []Capability{"platform.overview.read", "platform.configuration.read", "platform.configuration.manage", "platform.tasks.read", "platform.tasks.stop", "platform.relays.read", "platform.relays.manage", "platform.diagnostics.read"}
	auditor := capabilitiesWithSuffix(".read")
	auditor = append(auditor, "platform.audit.export")
	namespaceAll := capabilitiesWithPrefixes("namespace.")
	namespaceOperator := slices.DeleteFunc(append([]Capability(nil), namespaceAll...), func(value Capability) bool { return value == "namespace.authorization.delegate" })
	namespaceViewer := []Capability{CapabilityNamespaceAccess, "namespace.resources.read", "namespace.intercepts.read", "namespace.previews.read", "namespace.tasks.read", "namespace.authorization.read"}
	return []RoleDefinition{
		{ID: RolePlatformAdmin, DisplayName: "Platform Admin", BuiltIn: true, Statements: []Statement{allow(platform...)}},
		{ID: RoleSecurityAdmin, DisplayName: "Security Admin", BuiltIn: true, Statements: []Statement{allow(security...)}},
		{ID: RoleOperator, DisplayName: "Operator", BuiltIn: true, Statements: []Statement{allow(operator...)}},
		{ID: RoleAuditor, DisplayName: "Auditor", BuiltIn: true, Statements: []Statement{allow(auditor...)}},
		{ID: RoleNamespaceAdmin, DisplayName: "Namespace Admin", BuiltIn: true, Statements: []Statement{allow(namespaceAll...)}},
		{ID: RoleNamespaceOperator, DisplayName: "Namespace Operator", Delegatable: true, BuiltIn: true, Statements: []Statement{allow(namespaceOperator...)}},
		{ID: RoleNamespaceViewer, DisplayName: "Namespace Viewer", Delegatable: true, BuiltIn: true, Statements: []Statement{allow(namespaceViewer...)}},
	}
}

func capabilitiesWithPrefixes(prefixes ...string) []Capability {
	result := []Capability{}
	for _, capability := range capabilityCatalog {
		for _, prefix := range prefixes {
			if strings.HasPrefix(string(capability), prefix) {
				result = append(result, capability)
				break
			}
		}
	}
	return result
}
func capabilitiesWithSuffix(suffix string) []Capability {
	result := []Capability{}
	for _, c := range capabilityCatalog {
		if strings.HasSuffix(string(c), suffix) {
			result = append(result, c)
		}
	}
	return result
}

func capabilityForManagement(resource Resource, operation Operation) string {
	manage := operation != OperationRead && operation != OperationList
	switch resource {
	case ResourceStatus:
		return "platform.overview.read"
	case ResourceConfiguration:
		if manage {
			return "platform.configuration.manage"
		}
		return "platform.configuration.read"
	case ResourceProvider:
		if manage {
			return "platform.identity.providers.manage"
		}
		return "platform.identity.providers.read"
	case ResourceOAuthClient:
		if manage {
			return "platform.oauth-clients.manage"
		}
		return "platform.oauth-clients.read"
	case ResourcePrincipal, ResourceIdentityMapping:
		return "platform.identity.principals.read"
	case ResourceUser:
		if manage {
			return "platform.identity.users.manage"
		}
		return "platform.identity.users.read"
	case ResourcePolicy, ResourceAssignment:
		switch operation {
		case OperationPublish:
			return "platform.authorization.publish"
		case OperationRollback:
			return "platform.authorization.rollback"
		case OperationDryRun, OperationValidate:
			return "platform.authorization.simulate"
		}
		if manage {
			return "platform.authorization.manage"
		}
		return "platform.authorization.read"
	case ResourceSession:
		if manage {
			return "platform.sessions.revoke"
		}
		return "platform.sessions.read"
	case ResourceTask:
		if manage {
			return "platform.tasks.stop"
		}
		return "platform.tasks.read"
	case ResourceRelay:
		if manage {
			return "platform.relays.manage"
		}
		return "platform.relays.read"
	case ResourceAudit:
		if operation == OperationExport {
			return "platform.audit.export"
		}
		return "platform.audit.read"
	case ResourceDiagnostic, ResourceStorage, ResourceUpgrade:
		return "platform.diagnostics.read"
	case ResourceNamespaceMember, ResourceNamespacePolicy:
		if manage {
			return "namespace.authorization.delegate"
		}
		return "namespace.authorization.read"
	default:
		return ""
	}
}
