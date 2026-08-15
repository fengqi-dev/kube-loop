package authorization

const (
	CapabilityNamespaceAccess Capability = "namespace.access"
)

var capabilityCatalog = []Capability{
	"platform.overview.read", "platform.configuration.read", "platform.configuration.manage",
	"platform.identity.identities.read",
	"platform.identity.users.read", "platform.identity.users.manage", "platform.oauth-clients.read", "platform.oauth-clients.manage",
	"platform.sessions.read", "platform.sessions.revoke",
	"platform.tasks.read", "platform.tasks.stop", "platform.relays.read", "platform.relays.manage",
	"platform.audit.read", "platform.audit.export", "platform.diagnostics.read",
	"org.overview.read", "org.organization.read", "org.organization.manage", "org.identities.read", "org.identities.manage", "org.groups.read", "org.groups.manage",
	"org.invitations.manage", "org.oauth-clients.read", "org.oauth-clients.manage",
	"org.security.read", "org.security.manage", "org.audit.read",
	CapabilityNamespaceAccess, "namespace.resources.read", "namespace.exec.open", "namespace.port-forward.open",
	"namespace.files.download", "namespace.files.upload", "namespace.intercepts.read", "namespace.intercepts.manage",
	"namespace.previews.read", "namespace.previews.manage", "namespace.tasks.read", "namespace.tasks.stop",
}

var capabilitySet = func() map[Capability]struct{} {
	result := make(map[Capability]struct{}, len(capabilityCatalog))
	for _, capability := range capabilityCatalog {
		result[capability] = struct{}{}
	}
	return result
}()

// Capabilities returns the stable public capability catalog. The returned
// slice is detached so callers cannot mutate authorization process state.
func Capabilities() []Capability {
	return append([]Capability(nil), capabilityCatalog...)
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
	case ResourceOAuthClient:
		if manage {
			return "platform.oauth-clients.manage"
		}
		return "platform.oauth-clients.read"
	case ResourceIdentity:
		return "platform.identity.identities.read"
	case ResourceUser:
		if manage {
			return "platform.identity.users.manage"
		}
		return "platform.identity.users.read"
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
	default:
		return ""
	}
}
