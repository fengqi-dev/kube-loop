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
	case "admin_policy_revisions":
		if err := schemaVersion(2); err != nil {
			return err
		}
		revision, err := exportInteger(row[0])
		if err != nil || revision < 1 {
			return errors.New("policy revision must be positive")
		}
		value := AdminPolicyRevision{
			ID: exportText(row[1]), Spec: exportJSON(row[3]), ValidationState: exportText(row[5]),
			Validation: exportJSON(row[6]), CreatedBy: exportText(row[7]),
			CreatedAuthenticationType: exportText(row[8]), Reason: exportText(row[9]),
			CreatedAt: exportTime(row[10]),
		}
		if err := normalizeAdminPolicyRevision(&value); err != nil {
			return err
		}
		if value.SpecHash != exportText(row[4]) {
			return errors.New("policy revision hash does not match its canonical spec")
		}
		return nil
	case "provider_config_revisions":
		if err := schemaVersion(2); err != nil {
			return err
		}
		revision, err := exportInteger(row[0])
		if err != nil || revision < 1 {
			return errors.New("provider revision must be positive")
		}
		value := ProviderConfigRevision{
			ID: exportText(row[1]), ProviderID: exportText(row[3]), ProviderType: exportText(row[4]),
			Config: exportJSON(row[5]), SecretAliases: exportJSON(row[7]), ValidationState: exportText(row[8]),
			Validation: exportJSON(row[9]), CreatedBy: exportText(row[10]),
			CreatedAuthenticationType: exportText(row[11]), Reason: exportText(row[12]),
			CreatedAt: exportTime(row[13]),
		}
		if err := normalizeProviderConfigRevision(&value); err != nil {
			return err
		}
		if value.ConfigHash != exportText(row[6]) {
			return errors.New("provider revision hash does not match its canonical configuration")
		}
		return nil
	case "admin_assignments":
		if err := schemaVersion(1); err != nil {
			return err
		}
		policyRevision, err := exportInteger(row[2])
		if err != nil || policyRevision < 1 {
			return errors.New("assignment policy revision must be positive")
		}
		return normalizeAdminAssignment(&AdminAssignment{
			ID: exportText(row[0]), PolicyRevision: uint64(policyRevision), Role: exportText(row[3]),
			Subjects: exportJSON(row[4]), Groups: exportJSON(row[5]), Namespaces: exportJSON(row[6]),
			CreatedAt: exportTime(row[7]),
		})
	case "management_active_revisions":
		kind, configurationID, err := normalizeConfigurationIdentity(exportText(row[0]), exportText(row[1]))
		if err != nil || kind == "" || configurationID == "" {
			return errors.New("active management revision identity is invalid")
		}
		revision, revisionErr := exportInteger(row[2])
		etag, etagErr := exportInteger(row[3])
		if revisionErr != nil || etagErr != nil || revision < 1 || etag < 1 {
			return errors.New("active management revision values are invalid")
		}
		if _, _, err := normalizeManagementActor(exportText(row[4]), exportText(row[5])); err != nil {
			return err
		}
		return nil
	case "config_change_requests":
		if err := schemaVersion(1); err != nil {
			return err
		}
		baseRevision := int64(0)
		var err error
		if string(row[4]) != "null" {
			baseRevision, err = exportInteger(row[4])
			if err != nil || baseRevision < 1 {
				return errors.New("configuration change base revision is invalid")
			}
		}
		baseETag, err := exportInteger(row[5])
		if err != nil || baseETag < 0 {
			return errors.New("configuration change base ETag is invalid")
		}
		proposedRevision, err := exportInteger(row[6])
		if err != nil || proposedRevision < 1 {
			return errors.New("configuration change proposed revision is invalid")
		}
		status := exportText(row[7])
		if status != ChangeStatusDraft && status != ChangeStatusValidated && status != ChangeStatusPublished &&
			status != ChangeStatusRejected && status != ChangeStatusRolledBack {
			return errors.New("configuration change status is invalid")
		}
		value := ConfigChangeRequest{
			ID: exportText(row[0]), ConfigurationType: exportText(row[2]), ConfigurationID: exportText(row[3]),
			BaseRevision: uint64(baseRevision), BaseETag: uint64(baseETag), ProposedRevision: uint64(proposedRevision),
			Status: ChangeStatusDraft, IdempotencyHash: exportBinary(row[8]), RequestHash: exportText(row[9]),
			RequestedBy: exportText(row[10]), RequestedAuthenticationType: exportText(row[11]),
			Reason: exportText(row[12]), Validation: exportJSON(row[13]),
			CreatedAt: exportTime(row[14]), UpdatedAt: exportTime(row[15]),
		}
		return normalizeConfigChangeRequest(&value)
	case "relay_desired_states":
		if err := schemaVersion(1); err != nil {
			return err
		}
		version, err := exportInteger(row[3])
		if err != nil || version < 1 {
			return errors.New("Relay desired state version is invalid")
		}
		return normalizeRelayDesiredState(&RelayDesiredState{
			RelayID: exportText(row[0]), SchemaVersion: ObjectSchemaVersion, DesiredState: exportText(row[2]),
			Version: uint64(version), UpdatedBy: exportText(row[4]), UpdatedAuthenticationType: exportText(row[5]),
			Reason: exportText(row[6]), UpdatedAt: exportTime(row[7]),
		})
	case "audit_export_jobs":
		if err := schemaVersion(1); err != nil {
			return err
		}
		state, result, errorCode := exportText(row[2]), exportText(row[4]), exportText(row[5])
		if state != "pending" && state != "running" && state != "succeeded" && state != "failed" {
			return errors.New("audit export job state is invalid")
		}
		if !json.Valid(row[3]) || len(result) > maximumAuditExportBytes ||
			(state == "succeeded" && (result == "" || errorCode != "")) ||
			(state == "failed" && (result != "" || errorCode == "")) ||
			((state == "pending" || state == "running") && (result != "" || errorCode != "")) {
			return errors.New("audit export job outcome is invalid")
		}
		if _, _, err := normalizeManagementActor(exportText(row[6]), exportText(row[7])); err != nil {
			return err
		}
		if exportText(row[8]) == "" || !exportTime(row[11]).After(exportTime(row[9])) {
			return errors.New("audit export job lifetime is invalid")
		}
		return nil
	case "admin_sessions", "auth_attempts", "auth_exchanges":
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
