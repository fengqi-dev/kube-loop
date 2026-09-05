package storage

import "github.com/uptrace/bun"

type repositorySet struct {
	identities                 *identityRepository
	bootstrapTokens            *bootstrapTokenRepository
	credentials                *credentialRepository
	sessions                   *sessionRepository
	tasks                      TaskRepository
	resourceSnapshots          *resourceSnapshotRepository
	idempotency                *idempotencyRepository
	audit                      *auditRepository
	adminSessions              *adminSessionRepository
	oauthClients               *oauthClientRepository
	oauthSessions              *oauthSessionRepository
	oauthConsents              *oauthConsentRepository
	oauthAuthorizationRequests *oauthAuthorizationRequestRepository
	oauthBrowserSessions       *oauthBrowserSessionRepository
}

func newRepositorySet(
	backend Backend,
	executor sqlExecutor,
	orm bun.IDB,
) *repositorySet {
	base := repositoryBase{backend: backend, executor: executor, orm: orm}
	sessions := &sessionRepository{repositoryBase: base}
	audit := &auditRepository{repositoryBase: base}
	return &repositorySet{
		identities:      &identityRepository{repositoryBase: base},
		bootstrapTokens: &bootstrapTokenRepository{repositoryBase: base},
		credentials:     &credentialRepository{repositoryBase: base},
		sessions:        sessions,
		tasks: &auditedTaskRepository{
			delegate: &taskRepository{
				repositoryBase: base,
			}, sessions: sessions, audit: audit,
		},
		resourceSnapshots: &resourceSnapshotRepository{
			repositoryBase: base,
		},
		idempotency: &idempotencyRepository{
			repositoryBase: base,
		},
		audit: audit,
		adminSessions: &adminSessionRepository{
			repositoryBase: base,
		},
		oauthClients: &oauthClientRepository{
			repositoryBase: base,
		},
		oauthSessions: &oauthSessionRepository{
			repositoryBase: base,
		},
		oauthConsents: &oauthConsentRepository{
			repositoryBase: base,
		},
		oauthAuthorizationRequests: &oauthAuthorizationRequestRepository{
			repositoryBase: base,
		},
		oauthBrowserSessions: &oauthBrowserSessionRepository{
			repositoryBase: base,
		},
	}
}

func (repositories *repositorySet) setTaskTransactionManager(
	manager TransactionManager,
) {
	if tasks, ok := repositories.tasks.(*auditedTaskRepository); ok {
		tasks.transactions = manager
	}
}

func (repositories *repositorySet) Identities() IdentityRepository {
	return repositories.identities
}

func (repositories *repositorySet) BootstrapTokens() BootstrapTokenRepository {
	return repositories.bootstrapTokens
}

func (repositories *repositorySet) Credentials() CredentialRepository {
	return repositories.credentials
}

func (repositories *repositorySet) Sessions() SessionRepository {
	return repositories.sessions
}

func (repositories *repositorySet) Tasks() TaskRepository {
	return repositories.tasks
}

func (repositories *repositorySet) ResourceSnapshots() ResourceSnapshotRepository {
	return repositories.resourceSnapshots
}

func (repositories *repositorySet) Idempotency() IdempotencyRepository {
	return repositories.idempotency
}

func (repositories *repositorySet) Audit() AuditRepository {
	return repositories.audit
}

func (repositories *repositorySet) AdminSessions() AdminSessionRepository {
	return repositories.adminSessions
}

func (repositories *repositorySet) OAuthClients() OAuthClientRepository {
	return repositories.oauthClients
}

func (repositories *repositorySet) OAuthSessions() OAuthSessionRepository {
	return repositories.oauthSessions
}

func (repositories *repositorySet) OAuthConsents() OAuthConsentRepository {
	return repositories.oauthConsents
}

func (repositories *repositorySet) OAuthAuthorizationRequests() OAuthAuthorizationRequestRepository {
	return repositories.oauthAuthorizationRequests
}

func (repositories *repositorySet) OAuthBrowserSessions() OAuthBrowserSessionRepository {
	return repositories.oauthBrowserSessions
}
