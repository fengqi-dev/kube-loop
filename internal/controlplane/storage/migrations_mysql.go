package storage

import "strings"

var mysqlIndexedTextColumns = map[string]struct{}{
	"id": {}, "identity_id": {}, "provider_id": {}, "device_id": {},
	"cluster_id": {}, "state": {}, "type": {}, "idempotency_key": {}, "task_id": {},
	"kind": {}, "namespace": {}, "name": {}, "scope": {}, "key": {},
	"resource_type": {}, "resource_id": {}, "action": {}, "outcome": {}, "request_id": {},
	"organization_id": {}, "status": {}, "slug": {},
	"email": {}, "primary_email": {}, "scope_id": {},
	"relay_id": {}, "desired_state": {}, "username": {}, "client_id": {}, "scope_type": {},
	"authentication_type": {}, "updated_authentication_type": {},
	"requested_authentication_type": {},
	"requested_by":                  {},
	"authorization_id":              {},
}

var mysqlTimeColumns = map[string]struct{}{
	"created_at": {}, "updated_at": {}, "expires_at": {}, "revoked_at": {},
	"last_heartbeat_at": {}, "idle_expires_at": {}, "absolute_expires_at": {},
	"accepted_at": {}, "consumed_at": {},
	"authenticated_at": {}, "last_seen_at": {}, "auth_time": {},
}

func mysqlMigrationStatements(sqlite []string) []string {
	statements := make([]string, len(sqlite))
	for index, statement := range sqlite {
		statements[index] = mysqlMigrationStatement(statement)
	}
	return statements
}

func mysqlMigrationStatement(statement string) string {
	statement = strings.ReplaceAll(statement, "INTEGER PRIMARY KEY AUTOINCREMENT", "BIGINT AUTO_INCREMENT PRIMARY KEY")
	statement = strings.ReplaceAll(statement, "revision INTEGER", "revision BIGINT")
	statement = strings.ReplaceAll(statement, "BLOB", "VARBINARY(1024)")
	for column := range mysqlIndexedTextColumns {
		statement = replaceMySQLTextColumn(statement, column, "VARCHAR(128)")
	}
	for column := range mysqlTimeColumns {
		statement = replaceMySQLTextColumn(statement, column, "VARCHAR(64)")
	}
	statement = strings.ReplaceAll(statement, "_hash TEXT", "_hash VARCHAR(128)")
	statement = strings.ReplaceAll(statement, "_id TEXT", "_id VARCHAR(128)")
	statement = strings.ReplaceAll(statement, "TEXT", "LONGTEXT")
	statement = strings.ReplaceAll(statement, "DEFAULT ''", "DEFAULT ('')")
	statement = strings.ReplaceAll(statement, "DEFAULT '[]'", "DEFAULT ('[]')")
	statement = strings.ReplaceAll(statement, "DEFAULT '{}'", "DEFAULT ('{}')")
	statement = replaceMySQLIdentifier(statement, "key")
	return statement
}

func replaceMySQLTextColumn(statement, column, replacement string) string {
	needle := column + " TEXT"
	for offset := 0; offset < len(statement); {
		relativeIndex := strings.Index(statement[offset:], needle)
		if relativeIndex < 0 {
			return statement
		}
		index := offset + relativeIndex
		if index > 0 && isSQLIdentifierByte(statement[index-1]) {
			offset = index + len(column)
			continue
		}
		statement = statement[:index] + column + " " + replacement + statement[index+len(needle):]
		offset = index + len(column) + 1 + len(replacement)
	}
	return statement
}

func isSQLIdentifierByte(value byte) bool {
	return value == '_' || value == '`' || value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z' ||
		value >= '0' && value <= '9'
}

func replaceMySQLIdentifier(statement, identifier string) string {
	for offset := 0; offset < len(statement); {
		relativeIndex := strings.Index(statement[offset:], identifier)
		if relativeIndex < 0 {
			return statement
		}
		index := offset + relativeIndex
		end := index + len(identifier)
		if (index > 0 && isSQLIdentifierByte(statement[index-1])) ||
			(end < len(statement) && isSQLIdentifierByte(statement[end])) {
			offset = end
			continue
		}
		replacement := "`" + identifier + "`"
		statement = statement[:index] + replacement + statement[end:]
		offset = index + len(replacement)
	}
	return statement
}
