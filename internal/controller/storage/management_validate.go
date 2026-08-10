package storage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/remotetask"
)

func validateExportDomainRow(spec tableSpec, row []json.RawMessage) error {
	for index, column := range spec.columns {
		if len(column.name) < 3 || column.name[len(column.name)-3:] != "_at" || string(row[index]) == "null" {
			continue
		}
		value, err := time.Parse(time.RFC3339Nano, exportText(row[index]))
		_, offset := value.Zone()
		if err != nil || value.IsZero() || offset != 0 {
			return fmt.Errorf("%s must be a non-zero UTC RFC3339 timestamp", column.name)
		}
	}
	schemaVersion := func(index int) error {
		value, err := exportInteger(row[index])
		if err != nil || value != ObjectSchemaVersion {
			return fmt.Errorf("object schema version must be %d", ObjectSchemaVersion)
		}
		return nil
	}
	switch spec.name {
	case "principals":
		if err := schemaVersion(1); err != nil {
			return err
		}
		var groups []string
		if err := json.Unmarshal(row[6], &groups); err != nil {
			return errors.New("principal groups must be a JSON string array")
		}
		principal := Principal{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, Provider: exportText(row[2]),
			ExternalID: exportText(row[3]), DisplayName: exportText(row[4]), Email: exportText(row[5]), Groups: groups,
			CreatedAt: exportTime(row[7]), UpdatedAt: exportTime(row[8]),
		}
		return normalizePrincipal(&principal)
	case "token_families":
		if err := schemaVersion(1); err != nil {
			return err
		}
		family := TokenFamily{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, PrincipalID: exportText(row[2]),
			DeviceID: exportText(row[3]), RefreshTokenHash: exportBinary(row[4]), CreatedAt: exportTime(row[5]),
			ExpiresAt: exportTime(row[6]), RevokedAt: exportNullableTime(row[7]),
		}
		return normalizeTokenFamily(&family)
	case "refresh_tokens":
		record := RefreshTokenRecord{
			TokenHash: exportBinary(row[0]), FamilyID: exportText(row[1]), Status: exportText(row[2]),
			CreatedAt: exportTime(row[3]), UsedAt: exportNullableTime(row[4]),
		}
		return normalizeRefreshToken(&record)
	case "sessions":
		if err := schemaVersion(1); err != nil {
			return err
		}
		generation, err := exportInteger(row[6])
		if err != nil || generation < 1 {
			return errors.New("session generation must be positive")
		}
		session := Session{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, PrincipalID: exportText(row[2]),
			DeviceID: exportText(row[3]), ClusterID: exportText(row[4]), State: exportText(row[5]),
			Generation: uint64(generation), CreatedAt: exportTime(row[7]), UpdatedAt: exportTime(row[8]),
			ExpiresAt: exportTime(row[9]), Namespace: exportText(row[10]), LastHeartbeatAt: exportTime(row[11]),
			NetworkSpec: exportJSON(row[12]), NetworkSpecHash: exportText(row[13]),
		}
		return normalizeSession(&session)
	case "tasks":
		if err := schemaVersion(1); err != nil {
			return err
		}
		task := Task{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, PrincipalID: exportText(row[2]),
			SessionID: exportText(row[3]), Type: exportText(row[4]), State: remotetask.State(exportText(row[5])),
			Spec: exportJSON(row[6]), Result: exportJSON(row[7]), IdempotencyKey: exportText(row[8]),
			CreatedAt: exportTime(row[9]), UpdatedAt: exportTime(row[10]), ExpiresAt: exportNullableTime(row[11]),
		}
		return normalizeTask(&task)
	case "resource_snapshots":
		if err := schemaVersion(1); err != nil {
			return err
		}
		snapshot := ResourceSnapshot{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, TaskID: exportText(row[2]),
			Kind: exportText(row[3]), Namespace: exportText(row[4]), Name: exportText(row[5]),
			Data: exportJSON(row[6]), CreatedAt: exportTime(row[7]),
		}
		return normalizeResourceSnapshot(&snapshot)
	case "idempotency_records":
		if err := schemaVersion(0); err != nil {
			return err
		}
		record := IdempotencyRecord{
			SchemaVersion: ObjectSchemaVersion, Scope: exportText(row[1]), Key: exportText(row[2]),
			RequestHash: exportText(row[3]), ResourceType: exportText(row[4]), ResourceID: exportText(row[5]),
			Response: exportJSON(row[6]), CreatedAt: exportTime(row[7]), ExpiresAt: exportTime(row[8]),
		}
		return normalizeIdempotencyRecord(&record)
	case "audit_events":
		if err := schemaVersion(1); err != nil {
			return err
		}
		event := AuditEvent{
			ID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, PrincipalID: exportText(row[2]),
			Action: exportText(row[3]), ResourceType: exportText(row[4]), ResourceID: exportText(row[5]),
			Outcome: exportText(row[6]), RequestID: exportText(row[7]), Metadata: exportJSON(row[8]),
			CreatedAt: exportTime(row[9]),
		}
		return normalizeAuditEvent(&event)
	case "management_metadata":
		id, err := exportInteger(row[0])
		if err != nil || id != 1 {
			return errors.New("management metadata ID must be 1")
		}
		retiredAtNull := string(row[1]) == "null"
		revisionNull := string(row[2]) == "null"
		if retiredAtNull != revisionNull {
			return errors.New("management bootstrap retirement marker is incomplete")
		}
		if !revisionNull {
			revision, revisionErr := exportInteger(row[2])
			if revisionErr != nil || revision < 1 {
				return errors.New("management bootstrap retirement revision must be positive")
			}
		}
		return nil
	case "auth_attempts", "auth_exchanges":
		return nil
	default:
		return errors.New("unsupported storage export table")
	}
}

func exportText(raw json.RawMessage) string {
	if string(raw) == "null" {
		return ""
	}
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

func exportInteger(raw json.RawMessage) (int64, error) {
	var value int64
	err := json.Unmarshal(raw, &value)
	return value, err
}

func exportBinary(raw json.RawMessage) []byte {
	var value string
	_ = json.Unmarshal(raw, &value)
	decoded, _ := base64.StdEncoding.DecodeString(value)
	return decoded
}

func exportJSON(raw json.RawMessage) json.RawMessage {
	if string(raw) == "null" {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

func exportTime(raw json.RawMessage) time.Time {
	value := exportText(raw)
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func exportNullableTime(raw json.RawMessage) *time.Time {
	if string(raw) == "null" {
		return nil
	}
	value := exportTime(raw)
	return &value
}
