package storage

import (
	_ "embed"
	"strings"
)

const baselineSchemaVersion = 21

//go:embed schema.sqlite.sql
var sqliteBaselineSchema string

type migration struct {
	version    int
	sqlite     []string
	postgresql []string
	mysql      []string
}

var migrations = func() []migration {
	sqlite := splitSchema(sqliteBaselineSchema)
	return []migration{{
		version:    baselineSchemaVersion,
		sqlite:     sqlite,
		postgresql: postgresqlMigrationStatements(sqlite),
		mysql:      mysqlMigrationStatements(sqlite),
	}}
}()

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

func currentSchemaVersion() int {
	return baselineSchemaVersion
}
