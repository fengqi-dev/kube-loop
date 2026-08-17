package storage

import "strings"

var postgresqlBooleanColumns = map[string]struct{}{
	"enabled": {}, "public": {}, "trusted": {}, "builtin": {},
	"system_flag": {},
}

var postgresqlJSONColumns = map[string]struct{}{
	"network_spec_json": {}, "spec_json": {}, "result_json": {}, "data_json": {},
	"response_json": {}, "metadata_json": {}, "filter_json": {}, "redirect_uris_json": {},
	"grant_types_json": {}, "scopes_json": {}, "request_json": {},
}

var postgresqlBigIntColumns = map[string]struct{}{
	"id": {}, "generation": {}, "version": {}, "revision": {},
}

func postgresqlSchemaStatements(sqlite []string) []string {
	statements := make([]string, len(sqlite))
	for index, statement := range sqlite {
		statements[index] = postgresqlSchemaStatement(statement)
	}
	return statements
}

func postgresqlSchemaStatement(statement string) string {
	statement = strings.ReplaceAll(statement, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGSERIAL PRIMARY KEY")
	statement = strings.ReplaceAll(statement, "BLOB", "BYTEA")
	for _, column := range []string{"id_hash", "csrf_token_hash", "code_hash", "token_hash", "challenge_hash", "upstream_state_hash", "signature_hash", "scope_hash"} {
		statement = strings.ReplaceAll(statement, "length("+column+")", "octet_length("+column+")")
	}
	for column := range postgresqlBooleanColumns {
		statement = strings.ReplaceAll(statement, column+" INTEGER NOT NULL DEFAULT 1 CHECK ("+column+" IN (0, 1))", column+" BOOLEAN NOT NULL DEFAULT TRUE")
		statement = strings.ReplaceAll(statement, column+" INTEGER NOT NULL DEFAULT 0 CHECK ("+column+" IN (0, 1))", column+" BOOLEAN NOT NULL DEFAULT FALSE")
		statement = strings.ReplaceAll(statement, column+" INTEGER NOT NULL CHECK ("+column+" IN (0, 1))", column+" BOOLEAN NOT NULL")
	}
	for column := range postgresqlJSONColumns {
		statement = strings.ReplaceAll(statement, column+" TEXT NOT NULL DEFAULT '[]'", column+" JSONB NOT NULL DEFAULT '[]'::jsonb")
		statement = strings.ReplaceAll(statement, column+" TEXT NOT NULL DEFAULT '{}'", column+" JSONB NOT NULL DEFAULT '{}'::jsonb")
		statement = strings.ReplaceAll(statement, column+" TEXT NOT NULL", column+" JSONB NOT NULL")
		statement = strings.ReplaceAll(statement, column+" TEXT", column+" JSONB")
	}
	for column := range postgresqlBigIntColumns {
		statement = strings.ReplaceAll(statement, column+" INTEGER", column+" BIGINT")
	}
	return statement
}
