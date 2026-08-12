package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

type repositoryBase struct {
	backend  Backend
	executor sqlExecutor
	orm      bun.IDB
}

type sqlExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func (base repositoryBase) bind(query string) string {
	if base.backend != BackendPostgreSQL {
		return query
	}
	var builder strings.Builder
	parameter := 1
	for _, character := range query {
		if character == '?' {
			_, _ = fmt.Fprintf(&builder, "$%d", parameter)
			parameter++
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

func validateUUID(id, field string) error {
	if _, err := uuid.Parse(id); err != nil {
		return fmt.Errorf("%s must be a UUID", field)
	}
	return nil
}

func boundedLimit(limit int) (int, error) {
	if limit <= 0 {
		return 0, errors.New("cleanup limit must be positive")
	}
	if limit > 1000 {
		return 0, errors.New("cleanup limit must not exceed 1000")
	}
	return limit, nil
}

func rowsAffected(result sql.Result) (int64, error) {
	count, err := result.RowsAffected()
	if err != nil {
		return 0, databaseError("read affected storage rows", err)
	}
	return count, nil
}

// databaseOperationError keeps the driver error available to transaction
// retry classification without exposing database details through Error().
// In particular, PostgreSQL diagnostics can contain schema, table and query
// fragments that must not be returned by the API or written to normal logs.
type databaseOperationError struct {
	message string
	cause   error
}

func (err *databaseOperationError) Error() string { return err.message }
func (err *databaseOperationError) Unwrap() error { return err.cause }

func databaseError(message string, cause error) error {
	if cause == nil {
		return nil
	}
	return &databaseOperationError{message: message, cause: cause}
}

func mapWriteError(err error) error {
	if err == nil {
		return nil
	}
	if isConstraintError(err) {
		return ErrConflict
	}
	return databaseError("storage write failed", err)
}

func isRetryableTransactionError(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}

type repositorySet struct {
	principals                *principalRepository
	tokenFamilies             *tokenFamilyRepository
	refreshTokens             *refreshTokenRepository
	sessions                  *sessionRepository
	tasks                     TaskRepository
	resourceSnapshots         *resourceSnapshotRepository
	idempotency               *idempotencyRepository
	audit                     *auditRepository
	relayDesiredStates        *relayDesiredStateRepository
	auditExportJobs           *auditExportJobRepository
	authTransactions          *authTransactionRepository
	managementState           *managementStateRepository
	adminSessions             *adminSessionRepository
	adminPolicyRevisions      *adminPolicyRevisionRepository
	providerConfigRevisions   *providerConfigRevisionRepository
	adminAssignments          *adminAssignmentRepository
	activeManagementRevisions *activeManagementRevisionRepository
	configChangeRequests      *configChangeRequestRepository
}

func newRepositorySet(backend Backend, executor sqlExecutor, orm bun.IDB) *repositorySet {
	base := repositoryBase{backend: backend, executor: executor, orm: orm}
	sessions := &sessionRepository{repositoryBase: base}
	audit := &auditRepository{repositoryBase: base}
	return &repositorySet{
		principals:    &principalRepository{repositoryBase: base},
		tokenFamilies: &tokenFamilyRepository{repositoryBase: base},
		refreshTokens: &refreshTokenRepository{repositoryBase: base},
		sessions:      sessions,
		tasks: &auditedTaskRepository{
			delegate: &taskRepository{repositoryBase: base}, sessions: sessions, audit: audit,
		},
		resourceSnapshots:         &resourceSnapshotRepository{repositoryBase: base},
		idempotency:               &idempotencyRepository{repositoryBase: base},
		audit:                     audit,
		relayDesiredStates:        &relayDesiredStateRepository{repositoryBase: base},
		auditExportJobs:           &auditExportJobRepository{repositoryBase: base},
		authTransactions:          &authTransactionRepository{repositoryBase: base},
		managementState:           &managementStateRepository{repositoryBase: base},
		adminSessions:             &adminSessionRepository{repositoryBase: base},
		adminPolicyRevisions:      &adminPolicyRevisionRepository{repositoryBase: base},
		providerConfigRevisions:   &providerConfigRevisionRepository{repositoryBase: base},
		adminAssignments:          &adminAssignmentRepository{repositoryBase: base},
		activeManagementRevisions: &activeManagementRevisionRepository{repositoryBase: base},
		configChangeRequests:      &configChangeRequestRepository{repositoryBase: base},
	}
}

func (repositories *repositorySet) setTaskTransactionManager(manager TransactionManager) {
	if tasks, ok := repositories.tasks.(*auditedTaskRepository); ok {
		tasks.transactions = manager
	}
}

func (repositories *repositorySet) Principals() PrincipalRepository {
	return repositories.principals
}

func (repositories *repositorySet) TokenFamilies() TokenFamilyRepository {
	return repositories.tokenFamilies
}

func (repositories *repositorySet) RefreshTokens() RefreshTokenRepository {
	return repositories.refreshTokens
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

func (repositories *repositorySet) RelayDesiredStates() RelayDesiredStateRepository {
	return repositories.relayDesiredStates
}

func (repositories *repositorySet) AuditExportJobs() AuditExportJobRepository {
	return repositories.auditExportJobs
}

func (repositories *repositorySet) AuthTransactions() AuthTransactionRepository {
	return repositories.authTransactions
}

func (repositories *repositorySet) ManagementState() ManagementStateRepository {
	return repositories.managementState
}

func (repositories *repositorySet) AdminSessions() AdminSessionRepository {
	return repositories.adminSessions
}

func (repositories *repositorySet) AdminPolicyRevisions() AdminPolicyRevisionRepository {
	return repositories.adminPolicyRevisions
}

func (repositories *repositorySet) ProviderConfigRevisions() ProviderConfigRevisionRepository {
	return repositories.providerConfigRevisions
}

func (repositories *repositorySet) AdminAssignments() AdminAssignmentRepository {
	return repositories.adminAssignments
}

func (repositories *repositorySet) ActiveManagementRevisions() ActiveManagementRevisionRepository {
	return repositories.activeManagementRevisions
}

func (repositories *repositorySet) ConfigChangeRequests() ConfigChangeRequestRepository {
	return repositories.configChangeRequests
}
