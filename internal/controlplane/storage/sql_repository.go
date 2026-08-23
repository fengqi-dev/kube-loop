package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	mysqldriver "github.com/go-sql-driver/mysql"
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

type rowScanner interface {
	Scan(...any) error
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

func nullableBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}

func isConstraintError(err error) bool {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) &&
		strings.HasPrefix(postgresError.Code, "23") {
		return true
	}
	var mysqlError *mysqldriver.MySQLError
	if errors.As(err, &mysqlError) &&
		(mysqlError.Number == 1062 || mysqlError.Number == 1451 || mysqlError.Number == 1452 || mysqlError.Number == 3819) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unique constraint") ||
		strings.Contains(message, "duplicate key") ||
		strings.Contains(message, "constraint failed")
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

func expectOne(result sql.Result) error {
	count, err := rowsAffected(result)
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
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
	if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok {
		return postgresError.Code == "40001" || postgresError.Code == "40P01"
	}
	var mysqlError *mysqldriver.MySQLError
	return errors.As(err, &mysqlError) &&
		(mysqlError.Number == 1205 || mysqlError.Number == 1213)
}
