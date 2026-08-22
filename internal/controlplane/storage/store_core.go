package storage

import (
	"database/sql"
	"time"

	"github.com/uptrace/bun"
	_ "modernc.org/sqlite" // Register the pure-Go SQLite database/sql driver used by this backend.
)

type Store struct {
	db                      *sql.DB
	orm                     *bun.DB
	backend                 Backend
	queryTimeout            time.Duration
	transactionMaxRetries   int
	transactionRetryBackoff time.Duration
	repositories            *repositorySet
}

var _ Repositories = (*Store)(nil)
var _ TransactionManager = (*Store)(nil)
var _ ExplicitTransactionManager = (*Store)(nil)

type repositoryTransaction struct {
	transaction  bun.Tx
	repositories Repositories
}

func (transaction *repositoryTransaction) Repositories() Repositories {
	return transaction.repositories
}

func (transaction *repositoryTransaction) Commit() error { return transaction.transaction.Commit() }

func (transaction *repositoryTransaction) Rollback() error { return transaction.transaction.Rollback() }
