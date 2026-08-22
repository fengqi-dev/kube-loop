package storage

func (store *Store) Backend() Backend {
	return store.backend
}

func (store *Store) Identities() IdentityRepository {
	return store.repositories.Identities()
}

func (store *Store) BootstrapTokens() BootstrapTokenRepository {
	return store.repositories.BootstrapTokens()
}

func (store *Store) Credentials() CredentialRepository { return store.repositories.Credentials() }

func (store *Store) Sessions() SessionRepository {
	return store.repositories.Sessions()
}

func (store *Store) Tasks() TaskRepository {
	return store.repositories.Tasks()
}

func (store *Store) ResourceSnapshots() ResourceSnapshotRepository {
	return store.repositories.ResourceSnapshots()
}

func (store *Store) Idempotency() IdempotencyRepository {
	return store.repositories.Idempotency()
}

func (store *Store) Audit() AuditRepository {
	return store.repositories.Audit()
}

func (store *Store) RelayDesiredStates() RelayDesiredStateRepository {
	return store.repositories.RelayDesiredStates()
}

func (store *Store) AdminSessions() AdminSessionRepository {
	return store.repositories.AdminSessions()
}

func (store *Store) OAuthClients() OAuthClientRepository { return store.repositories.OAuthClients() }

func (store *Store) OAuthSessions() OAuthSessionRepository { return store.repositories.OAuthSessions() }

func (store *Store) OAuthConsents() OAuthConsentRepository { return store.repositories.OAuthConsents() }

func (store *Store) OAuthAuthorizationRequests() OAuthAuthorizationRequestRepository {
	return store.repositories.OAuthAuthorizationRequests()
}
func (store *Store) OAuthBrowserSessions() OAuthBrowserSessionRepository {
	return store.repositories.OAuthBrowserSessions()
}
