package storage

import (
	_ "embed"
	"strings"
)

const (
	currentSchemaID  = "kubeloop-v2"
	previousSchemaID = "kubeloop-v1"
)

//go:embed schema.sqlite.sql
var sqliteBaselineSchema string

func schemaStatements(backend Backend) []string {
	sqlite := splitSchema(sqliteBaselineSchema)
	switch backend {
	case BackendSQLite:
		return sqlite
	case BackendPostgreSQL:
		return postgresqlSchemaStatements(sqlite)
	case BackendMySQL:
		return mysqlSchemaStatements(sqlite)
	}
	return sqlite
}

func splitSchema(schema string) []string {
	parts := strings.Split(schema, ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		if statement := strings.TrimSpace(part); statement != "" {
			statements = append(statements, statement)
		}
	}
	return statements
}
