package authorization

type scope uint8

const (
	scopeCluster scope = 1 << iota
	scopeNamespace
	scopeAny = scopeCluster | scopeNamespace
)

type permission struct {
	resource  Resource
	operation Operation
	scope     scope
}

var resourceScopes = map[Resource]scope{
	ResourceStatus:          scopeCluster,
	ResourceConfiguration:   scopeCluster,
	ResourceProvider:        scopeCluster,
	ResourceAssignment:      scopeCluster,
	ResourcePolicy:          scopeCluster,
	ResourceIdentityMapping: scopeCluster,
	ResourcePrincipal:       scopeCluster,
	ResourceSession:         scopeAny,
	ResourceTask:            scopeAny,
	ResourceRelay:           scopeCluster,
	ResourceAudit:           scopeCluster,
	ResourceDiagnostic:      scopeCluster,
	ResourceStorage:         scopeCluster,
	ResourceUpgrade:         scopeCluster,
	ResourceNamespaceMember: scopeNamespace,
	ResourceNamespacePolicy: scopeNamespace,
}

var rolePermissions = compilePermissions(map[Role][]permission{
	RolePlatformAdmin: {
		{ResourceStatus, OperationRead, scopeCluster},
		{ResourceConfiguration, OperationRead, scopeCluster}, {ResourceConfiguration, OperationUpdate, scopeCluster},
		{ResourceConfiguration, OperationPublish, scopeCluster}, {ResourceConfiguration, OperationRollback, scopeCluster},
		{ResourceProvider, OperationRead, scopeCluster}, {ResourceProvider, OperationList, scopeCluster},
		{ResourceProvider, OperationCreate, scopeCluster}, {ResourceProvider, OperationUpdate, scopeCluster},
		{ResourceProvider, OperationDelete, scopeCluster}, {ResourceProvider, OperationValidate, scopeCluster},
		{ResourceProvider, OperationPublish, scopeCluster}, {ResourceProvider, OperationRollback, scopeCluster},
		{ResourceAssignment, OperationRead, scopeCluster}, {ResourceAssignment, OperationList, scopeCluster},
		{ResourceAssignment, OperationCreate, scopeCluster}, {ResourceAssignment, OperationUpdate, scopeCluster},
		{ResourceAssignment, OperationDelete, scopeCluster}, {ResourceAssignment, OperationPublish, scopeCluster},
		{ResourceAssignment, OperationRollback, scopeCluster},
		{ResourcePolicy, OperationRead, scopeCluster}, {ResourcePolicy, OperationList, scopeCluster},
		{ResourcePolicy, OperationCreate, scopeCluster}, {ResourcePolicy, OperationUpdate, scopeCluster},
		{ResourcePolicy, OperationDelete, scopeCluster}, {ResourcePolicy, OperationValidate, scopeCluster},
		{ResourcePolicy, OperationDryRun, scopeCluster}, {ResourcePolicy, OperationPublish, scopeCluster},
		{ResourcePolicy, OperationRollback, scopeCluster},
		{ResourceIdentityMapping, OperationRead, scopeCluster}, {ResourceIdentityMapping, OperationList, scopeCluster},
		{ResourceIdentityMapping, OperationCreate, scopeCluster}, {ResourceIdentityMapping, OperationUpdate, scopeCluster},
		{ResourceIdentityMapping, OperationDelete, scopeCluster}, {ResourceIdentityMapping, OperationPublish, scopeCluster},
		{ResourceIdentityMapping, OperationRollback, scopeCluster},
		{ResourcePrincipal, OperationRead, scopeCluster}, {ResourcePrincipal, OperationList, scopeCluster},
		{ResourceSession, OperationRead, scopeAny}, {ResourceSession, OperationList, scopeAny},
		{ResourceSession, OperationRevoke, scopeAny}, {ResourceSession, OperationStop, scopeAny},
		{ResourceTask, OperationRead, scopeAny}, {ResourceTask, OperationList, scopeAny},
		{ResourceTask, OperationStop, scopeAny}, {ResourceTask, OperationRecover, scopeAny},
		{ResourceRelay, OperationRead, scopeCluster}, {ResourceRelay, OperationList, scopeCluster},
		{ResourceRelay, OperationDrain, scopeCluster}, {ResourceRelay, OperationRecover, scopeCluster},
		{ResourceAudit, OperationRead, scopeCluster}, {ResourceAudit, OperationList, scopeCluster},
		{ResourceAudit, OperationExport, scopeCluster},
		{ResourceDiagnostic, OperationRead, scopeCluster}, {ResourceDiagnostic, OperationCreate, scopeCluster},
		{ResourceDiagnostic, OperationExport, scopeCluster},
		{ResourceStorage, OperationRead, scopeCluster},
		{ResourceUpgrade, OperationRead, scopeCluster}, {ResourceUpgrade, OperationUpdate, scopeCluster},
		{ResourceNamespaceMember, OperationRead, scopeNamespace}, {ResourceNamespaceMember, OperationList, scopeNamespace},
		{ResourceNamespaceMember, OperationCreate, scopeNamespace}, {ResourceNamespaceMember, OperationUpdate, scopeNamespace},
		{ResourceNamespaceMember, OperationDelete, scopeNamespace},
		{ResourceNamespacePolicy, OperationRead, scopeNamespace}, {ResourceNamespacePolicy, OperationList, scopeNamespace},
		{ResourceNamespacePolicy, OperationCreate, scopeNamespace}, {ResourceNamespacePolicy, OperationUpdate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDelete, scopeNamespace}, {ResourceNamespacePolicy, OperationValidate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDryRun, scopeNamespace}, {ResourceNamespacePolicy, OperationPublish, scopeNamespace},
		{ResourceNamespacePolicy, OperationRollback, scopeNamespace},
	},
	RoleSecurityAdmin: {
		{ResourceStatus, OperationRead, scopeCluster},
		{ResourceConfiguration, OperationRead, scopeCluster},
		{ResourceProvider, OperationRead, scopeCluster}, {ResourceProvider, OperationList, scopeCluster},
		{ResourceAssignment, OperationRead, scopeCluster}, {ResourceAssignment, OperationList, scopeCluster},
		{ResourcePolicy, OperationRead, scopeCluster}, {ResourcePolicy, OperationList, scopeCluster},
		{ResourcePolicy, OperationCreate, scopeCluster}, {ResourcePolicy, OperationUpdate, scopeCluster},
		{ResourcePolicy, OperationDelete, scopeCluster}, {ResourcePolicy, OperationValidate, scopeCluster},
		{ResourcePolicy, OperationDryRun, scopeCluster}, {ResourcePolicy, OperationPublish, scopeCluster},
		{ResourcePolicy, OperationRollback, scopeCluster},
		{ResourceIdentityMapping, OperationRead, scopeCluster}, {ResourceIdentityMapping, OperationList, scopeCluster},
		{ResourceIdentityMapping, OperationCreate, scopeCluster}, {ResourceIdentityMapping, OperationUpdate, scopeCluster},
		{ResourceIdentityMapping, OperationDelete, scopeCluster}, {ResourceIdentityMapping, OperationPublish, scopeCluster},
		{ResourceIdentityMapping, OperationRollback, scopeCluster},
		{ResourcePrincipal, OperationRead, scopeCluster}, {ResourcePrincipal, OperationList, scopeCluster},
		{ResourceSession, OperationRead, scopeAny}, {ResourceSession, OperationList, scopeAny},
		{ResourceSession, OperationRevoke, scopeAny},
		{ResourceTask, OperationRead, scopeAny}, {ResourceTask, OperationList, scopeAny},
		{ResourceAudit, OperationRead, scopeCluster}, {ResourceAudit, OperationList, scopeCluster},
		{ResourceAudit, OperationExport, scopeCluster},
		{ResourceNamespaceMember, OperationRead, scopeNamespace}, {ResourceNamespaceMember, OperationList, scopeNamespace},
		{ResourceNamespaceMember, OperationCreate, scopeNamespace}, {ResourceNamespaceMember, OperationUpdate, scopeNamespace},
		{ResourceNamespaceMember, OperationDelete, scopeNamespace},
		{ResourceNamespacePolicy, OperationRead, scopeNamespace}, {ResourceNamespacePolicy, OperationList, scopeNamespace},
		{ResourceNamespacePolicy, OperationCreate, scopeNamespace}, {ResourceNamespacePolicy, OperationUpdate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDelete, scopeNamespace}, {ResourceNamespacePolicy, OperationValidate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDryRun, scopeNamespace}, {ResourceNamespacePolicy, OperationPublish, scopeNamespace},
		{ResourceNamespacePolicy, OperationRollback, scopeNamespace},
	},
	RoleOperator: {
		{ResourceStatus, OperationRead, scopeCluster},
		{ResourceSession, OperationRead, scopeAny}, {ResourceSession, OperationList, scopeAny},
		{ResourceSession, OperationStop, scopeAny},
		{ResourceTask, OperationRead, scopeAny}, {ResourceTask, OperationList, scopeAny},
		{ResourceTask, OperationStop, scopeAny}, {ResourceTask, OperationRecover, scopeAny},
		{ResourceRelay, OperationRead, scopeCluster}, {ResourceRelay, OperationList, scopeCluster},
		{ResourceRelay, OperationDrain, scopeCluster}, {ResourceRelay, OperationRecover, scopeCluster},
		{ResourceDiagnostic, OperationRead, scopeCluster}, {ResourceDiagnostic, OperationCreate, scopeCluster},
		{ResourceDiagnostic, OperationExport, scopeCluster},
	},
	RoleAuditor: {
		{ResourceStatus, OperationRead, scopeCluster},
		{ResourceConfiguration, OperationRead, scopeCluster},
		{ResourceProvider, OperationRead, scopeCluster}, {ResourceProvider, OperationList, scopeCluster},
		{ResourceAssignment, OperationRead, scopeCluster}, {ResourceAssignment, OperationList, scopeCluster},
		{ResourcePolicy, OperationRead, scopeCluster}, {ResourcePolicy, OperationList, scopeCluster},
		{ResourceIdentityMapping, OperationRead, scopeCluster}, {ResourceIdentityMapping, OperationList, scopeCluster},
		{ResourcePrincipal, OperationRead, scopeCluster}, {ResourcePrincipal, OperationList, scopeCluster},
		{ResourceSession, OperationRead, scopeAny}, {ResourceSession, OperationList, scopeAny},
		{ResourceTask, OperationRead, scopeAny}, {ResourceTask, OperationList, scopeAny},
		{ResourceRelay, OperationRead, scopeCluster}, {ResourceRelay, OperationList, scopeCluster},
		{ResourceAudit, OperationRead, scopeCluster}, {ResourceAudit, OperationList, scopeCluster},
		{ResourceStorage, OperationRead, scopeCluster},
		{ResourceUpgrade, OperationRead, scopeCluster},
		{ResourceNamespaceMember, OperationRead, scopeNamespace}, {ResourceNamespaceMember, OperationList, scopeNamespace},
		{ResourceNamespacePolicy, OperationRead, scopeNamespace}, {ResourceNamespacePolicy, OperationList, scopeNamespace},
	},
	RoleNamespaceAdmin: {
		{ResourceNamespaceMember, OperationRead, scopeNamespace}, {ResourceNamespaceMember, OperationList, scopeNamespace},
		{ResourceNamespaceMember, OperationCreate, scopeNamespace}, {ResourceNamespaceMember, OperationUpdate, scopeNamespace},
		{ResourceNamespaceMember, OperationDelete, scopeNamespace},
		{ResourceNamespacePolicy, OperationRead, scopeNamespace}, {ResourceNamespacePolicy, OperationList, scopeNamespace},
		{ResourceNamespacePolicy, OperationCreate, scopeNamespace}, {ResourceNamespacePolicy, OperationUpdate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDelete, scopeNamespace}, {ResourceNamespacePolicy, OperationValidate, scopeNamespace},
		{ResourceNamespacePolicy, OperationDryRun, scopeNamespace}, {ResourceNamespacePolicy, OperationPublish, scopeNamespace},
		{ResourceNamespacePolicy, OperationRollback, scopeNamespace},
		{ResourceSession, OperationRead, scopeNamespace}, {ResourceSession, OperationList, scopeNamespace},
		{ResourceTask, OperationRead, scopeNamespace}, {ResourceTask, OperationList, scopeNamespace},
	},
})

func compilePermissions(source map[Role][]permission) map[Role]map[Resource]map[Operation]scope {
	result := make(map[Role]map[Resource]map[Operation]scope, len(source))
	for role, permissions := range source {
		resources := make(map[Resource]map[Operation]scope)
		for _, item := range permissions {
			operations := resources[item.resource]
			if operations == nil {
				operations = make(map[Operation]scope)
				resources[item.resource] = operations
			}
			operations[item.operation] |= item.scope
		}
		result[role] = resources
	}
	return result
}

func roleAllows(role Role, request Request) bool {
	operations := rolePermissions[role][request.Resource]
	allowedScope := operations[request.Operation]
	requestedScope := scopeCluster
	if request.Namespace != "" {
		requestedScope = scopeNamespace
	}
	return allowedScope&requestedScope != 0
}
