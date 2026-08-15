package storage

import "strings"

var mysqlIndexedTextColumns = map[string]struct{}{
	"id": {}, "identity_id": {}, "provider_id": {}, "subject": {}, "device_id": {},
	"cluster_id": {}, "state": {}, "type": {}, "idempotency_key": {}, "task_id": {},
	"kind": {}, "namespace": {}, "name": {}, "scope": {}, "key": {},
	"resource_type": {}, "resource_id": {}, "action": {}, "outcome": {}, "request_id": {},
	"organization_id": {}, "status": {}, "slug": {}, "domain": {}, "role_id": {},
	"email": {}, "primary_email": {}, "scope_id": {},
	"relay_id": {}, "desired_state": {}, "username": {}, "client_id": {}, "scope_type": {},
	"authentication_type": {}, "created_authentication_type": {}, "updated_authentication_type": {},
	"requested_authentication_type": {}, "validation_state": {}, "provider_type": {},
	"requested_by":     {},
	"authorization_id": {},
	"subject_type":     {},
}

var mysqlTimeColumns = map[string]struct{}{
	"created_at": {}, "updated_at": {}, "expires_at": {}, "revoked_at": {}, "used_at": {},
	"last_heartbeat_at": {}, "idle_expires_at": {}, "absolute_expires_at": {},
	"bootstrap_retired_at": {}, "verified_at": {}, "accepted_at": {}, "consumed_at": {},
	"authenticated_at": {}, "last_used_at": {},
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
	statement = strings.ReplaceAll(statement, "policy_revision INTEGER", "policy_revision BIGINT")
	statement = strings.ReplaceAll(statement, "BLOB", "VARBINARY(1024)")
	lines := strings.Split(statement, "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		fields := strings.Fields(trimmed)
		if len(fields) < 2 || !strings.Contains(line, "TEXT") {
			continue
		}
		column := strings.Trim(fields[0], "`,")
		for fieldIndex, field := range fields {
			if strings.EqualFold(field, "COLUMN") && fieldIndex+1 < len(fields) {
				column = strings.Trim(fields[fieldIndex+1], "`,")
				break
			}
		}
		replacement := "LONGTEXT"
		if _, ok := mysqlIndexedTextColumns[column]; ok || strings.HasSuffix(column, "_id") {
			replacement = "VARCHAR(128)"
		} else if _, ok := mysqlTimeColumns[column]; ok {
			replacement = "VARCHAR(64)"
		} else if strings.HasSuffix(column, "_hash") {
			replacement = "VARCHAR(128)"
		}
		lines[index] = strings.Replace(line, "TEXT", replacement, 1)
		if column == "key" {
			lines[index] = strings.Replace(lines[index], fields[0], "`key`", 1)
		}
		if replacement == "LONGTEXT" {
			lines[index] = strings.ReplaceAll(lines[index], "DEFAULT ''", "DEFAULT ('')")
			lines[index] = strings.ReplaceAll(lines[index], "DEFAULT '[]'", "DEFAULT ('[]')")
			lines[index] = strings.ReplaceAll(lines[index], "DEFAULT '{}'", "DEFAULT ('{}')")
		}
	}
	statement = strings.Join(lines, "\n")
	statement = strings.ReplaceAll(statement, "(scope, key)", "(scope, `key`)")
	return statement
}
