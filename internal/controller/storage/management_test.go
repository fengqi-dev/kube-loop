package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
)

func TestSQLiteLogicalExportIsDeterministicAndValidated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	store := openSQLiteTestStore(t, path)
	seed := seedManagementStore(t, store)
	config := Config{Backend: BackendSQLite, SQLitePath: path}
	createdAt := time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)
	options := ExportOptions{CreatedByVersion: "kubeloop-controller/test", Now: func() time.Time { return createdAt }}

	first, metadata, err := Export(context.Background(), config, options)
	if err != nil {
		t.Fatal(err)
	}
	second, secondMetadata, err := Export(context.Background(), config, options)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) || metadata != secondMetadata {
		t.Fatal("unchanged storage produced a non-deterministic export")
	}
	if metadata.SchemaVersion != currentSchemaVersion() || metadata.SourceBackend != BackendSQLite ||
		metadata.CreatedAt != createdAt || metadata.Rows != len(exportTableSpecs)-3 || len(metadata.ChecksumSHA256) != 64 {
		t.Fatalf("export metadata = %#v", metadata)
	}
	validated, err := ValidateExport(first)
	if err != nil || validated != metadata {
		t.Fatalf("validated metadata = %#v, error = %v", validated, err)
	}
	var document ExportDocument
	if err := json.Unmarshal(first, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Tables) != len(exportTableSpecs) {
		t.Fatalf("tables = %d", len(document.Tables))
	}
	for index, table := range document.Tables {
		wantRows := 1
		if exportTableSpecs[index].omitRows {
			wantRows = 0
		}
		if table.Name != exportTableSpecs[index].name || len(table.Rows) != wantRows {
			t.Fatalf("table %d = %#v", index, table)
		}
	}
	if !bytes.Contains(first, []byte(seed.principalID)) {
		t.Fatal("logical export omitted seeded rows")
	}

	tampered := bytes.Replace(first, []byte("management-user"), []byte("management-root"), 1)
	if _, err := ValidateExport(tampered); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("tampered export validation error = %v", err)
	}
	t.Setenv("KUBELOOP_POSTGRESQL_DSN", "postgres://database-secret-marker")
	t.Setenv("KUBELOOP_OIDC_CLIENT_SECRET", "oidc-secret-marker")
	t.Setenv("KUBELOOP_AD_PASSWORD", "ad-secret-marker")
	for _, secret := range []string{"database-secret-marker", "oidc-secret-marker", "ad-secret-marker"} {
		if bytes.Contains(first, []byte(secret)) {
			t.Fatalf("export contains configuration secret %q", secret)
		}
	}
	for _, transientSecret := range []string{
		"client-state", `"verifier"`, "management-auth-state", "management-exchange-code",
		"management-browser-session", "management-browser-csrf",
	} {
		if bytes.Contains(first, []byte(transientSecret)) {
			t.Fatalf("export contains transient authentication secret %q", transientSecret)
		}
	}
}

func TestValidateExportRejectsWrongSchemaInventoryAndTrailingData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	store := openSQLiteTestStore(t, path)
	seedManagementStore(t, store)
	raw, _, err := Export(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}, ExportOptions{
		CreatedByVersion: "kubeloop-controller/test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document ExportDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document.SchemaVersion++
	wrongSchema, _, err := marshalExport(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateExport(wrongSchema); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("wrong schema validation error = %v", err)
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document.Tables[0].Rows[0][1] = json.RawMessage(`2`)
	wrongObjectSchema, _, err := marshalExport(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateExport(wrongObjectSchema); err == nil || !strings.Contains(err.Error(), "object schema version") {
		t.Fatalf("wrong object schema validation error = %v", err)
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document.Tables[len(document.Tables)-2].Rows = [][]json.RawMessage{{}}
	transientAuthData, _, err := marshalExport(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateExport(transientAuthData); err == nil || !strings.Contains(err.Error(), "transient authentication") {
		t.Fatalf("transient authentication validation error = %v", err)
	}

	if _, err := ValidateExport(append(raw, []byte(`{"extra":true}`)...)); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing data validation error = %v", err)
	}
	if _, err := ValidateExport(raw[:len(raw)/2]); err == nil {
		t.Fatal("truncated export passed validation")
	}
}

func TestSQLiteConsistentBackup(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	store := openSQLiteTestStore(t, source)
	seed := seedManagementStore(t, store)
	destination := filepath.Join(directory, "backups", "snapshot.db")
	createdAt := time.Date(2026, 8, 10, 13, 0, 0, 0, time.UTC)

	result, err := BackupSQLite(
		context.Background(),
		Config{Backend: BackendSQLite, SQLitePath: source},
		destination,
		func() time.Time { return createdAt },
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != destination || result.SchemaVersion != currentSchemaVersion() || result.CreatedAt != createdAt ||
		result.Bytes <= 0 || len(result.ChecksumSHA256) != 64 {
		t.Fatalf("backup result = %#v", result)
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode = %o", info.Mode().Perm())
	}
	backup := openSQLiteTestStore(t, destination)
	principal, err := backup.Principals().GetByID(context.Background(), seed.principalID)
	if err != nil || principal.ExternalID != "management-user" {
		t.Fatalf("backed up principal = %#v, error = %v", principal, err)
	}
	if retired, err := backup.ManagementState().BootstrapRetired(context.Background()); err != nil || !retired {
		t.Fatalf("backed up bootstrap retirement = %t, error = %v", retired, err)
	}
	if _, err := BackupSQLite(
		context.Background(), Config{Backend: BackendSQLite, SQLitePath: source}, destination, nil,
	); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing destination error = %v", err)
	}
}

func TestManagementOperationsRejectUnsafeInputs(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.db")
	if _, _, err := Export(context.Background(), Config{Backend: BackendSQLite, SQLitePath: missing}, ExportOptions{
		CreatedByVersion: "test",
	}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing export source error = %v", err)
	}
	if _, err := Import(context.Background(), Config{Backend: BackendSQLite, SQLitePath: missing}, []byte(`{}`), ImportOptions{}); err == nil ||
		!strings.Contains(err.Error(), "confirmation") {
		t.Fatalf("unconfirmed import error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "source.db")
	store := openSQLiteTestStore(t, path)
	seedManagementStore(t, store)
	raw, _, err := Export(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}, ExportOptions{CreatedByVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Import(context.Background(), Config{Backend: BackendSQLite, SQLitePath: path}, raw, ImportOptions{
		ConfirmedEmpty: true, ImportedBy: "test",
	}); err == nil || !strings.Contains(err.Error(), "PostgreSQL") {
		t.Fatalf("SQLite logical import error = %v", err)
	}
}

func TestPostgreSQLLogicalImportAndAuditIntegration(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source := openSQLiteTestStore(t, sourcePath)
	seed := seedManagementStore(t, source)
	createdAt := time.Date(2026, 8, 10, 14, 0, 0, 0, time.UTC)
	raw, metadata, err := Export(context.Background(), Config{Backend: BackendSQLite, SQLitePath: sourcePath}, ExportOptions{
		CreatedByVersion: "kubeloop-controller/source", Now: func() time.Time { return createdAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	targetConfig, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	importedAt := createdAt.Add(time.Hour)
	result, err := Import(context.Background(), targetConfig, raw, ImportOptions{
		ConfirmedEmpty: true, ImportedBy: "operator@example.invalid", Now: func() time.Time { return importedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.SchemaVersion != currentSchemaVersion() || result.ImportedAt != importedAt ||
		result.ImportedBy != "operator@example.invalid" || result.ChecksumSHA256 != metadata.ChecksumSHA256 ||
		result.Rows != metadata.Rows || result.AuditEventID == "" {
		t.Fatalf("import result = %#v", result)
	}
	target, err := Open(context.Background(), targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	principal, err := target.Principals().GetByID(context.Background(), seed.principalID)
	if err != nil || principal.ExternalID != "management-user" || !strings.EqualFold(principal.Email, "management@example.invalid") {
		t.Fatalf("imported principal = %#v, error = %v", principal, err)
	}
	if _, err := target.Sessions().GetByID(context.Background(), seed.sessionID); err != nil {
		t.Fatalf("read imported session: %v", err)
	}
	if _, err := target.Tasks().GetByID(context.Background(), seed.taskID); err != nil {
		t.Fatalf("read imported task: %v", err)
	}
	if retired, err := target.ManagementState().BootstrapRetired(context.Background()); err != nil || !retired {
		t.Fatalf("imported bootstrap retirement = %t, error = %v", retired, err)
	}
	events, err := target.Audit().List(context.Background(), AuditFilter{Action: "storage.import", Limit: 10})
	if err != nil || len(events) != 1 || events[0].ID != result.AuditEventID || events[0].Outcome != "success" ||
		!bytes.Contains(events[0].Metadata, []byte(metadata.ChecksumSHA256)) {
		t.Fatalf("import audit events = %#v, error = %v", events, err)
	}
	if _, err := Import(context.Background(), targetConfig, raw, ImportOptions{
		ConfirmedEmpty: true, ImportedBy: "operator@example.invalid",
	}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("non-empty import error = %v", err)
	}

	postgresExport, postgresMetadata, err := Export(context.Background(), targetConfig, ExportOptions{
		CreatedByVersion: "kubeloop-controller/postgresql", Now: func() time.Time { return importedAt.Add(time.Hour) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if postgresMetadata.SourceBackend != BackendPostgreSQL || postgresMetadata.Rows != metadata.Rows+1 {
		t.Fatalf("PostgreSQL export metadata = %#v", postgresMetadata)
	}
	if _, err := ValidateExport(postgresExport); err != nil {
		t.Fatalf("validate PostgreSQL export: %v", err)
	}
}

func TestPostgreSQLLogicalImportRollsBackIntegration(t *testing.T) {
	sourcePath := filepath.Join(t.TempDir(), "source.db")
	source := openSQLiteTestStore(t, sourcePath)
	seedManagementStore(t, source)
	raw, _, err := Export(context.Background(), Config{Backend: BackendSQLite, SQLitePath: sourcePath}, ExportOptions{
		CreatedByVersion: "kubeloop-controller/source",
	})
	if err != nil {
		t.Fatal(err)
	}
	var document ExportDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	invalidPrincipal, _ := json.Marshal(uuid.NewString())
	document.Tables[1].Rows[0][2] = invalidPrincipal
	tampered, _, err := marshalExport(document)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateExport(tampered); err != nil {
		t.Fatalf("structurally valid rollback fixture: %v", err)
	}
	targetConfig, cleanup := newPostgreSQLIntegrationConfig(t)
	defer cleanup()
	if _, err := Import(context.Background(), targetConfig, tampered, ImportOptions{
		ConfirmedEmpty: true, ImportedBy: "rollback-test",
	}); err == nil || !strings.Contains(err.Error(), "token_families") {
		t.Fatalf("invalid relationship import error = %v", err)
	}
	target, err := Open(context.Background(), targetConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	for _, spec := range exportTableSpecs {
		var count int
		if err := target.db.QueryRow(`SELECT COUNT(*) FROM ` + spec.name).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("failed import left %d rows in %s", count, spec.name)
		}
	}
}

type managementSeed struct {
	principalID string
	sessionID   string
	taskID      string
}

func seedManagementStore(t *testing.T, store *Store) managementSeed {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 10, 11, 0, 0, 0, time.UTC)
	principalID := uuid.NewString()
	principal, err := store.Principals().Upsert(ctx, Principal{
		ID: principalID, Provider: "oidc", ExternalID: "management-user", DisplayName: "Management User",
		Email: "management@example.invalid", Groups: []string{"operators"}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	tokenHash := sha256.Sum256([]byte("management-refresh-token"))
	familyID := uuid.NewString()
	if err := store.TokenFamilies().Create(ctx, TokenFamily{
		ID: familyID, PrincipalID: principal.ID, DeviceID: "management-device",
		RefreshTokenHash: tokenHash[:], CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.RefreshTokens().Create(ctx, RefreshTokenRecord{
		TokenHash: tokenHash[:], FamilyID: familyID, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	sessionID := uuid.NewString()
	if err := store.Sessions().Create(ctx, Session{
		ID: sessionID, PrincipalID: principal.ID, DeviceID: "management-device", ClusterID: "cluster-a",
		Namespace: "default", State: "active", NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	taskID := uuid.NewString()
	if err := store.Tasks().Create(ctx, Task{
		ID: taskID, PrincipalID: principal.ID, SessionID: sessionID, Type: "port-forward",
		State: remotetask.Running, Spec: json.RawMessage(`{"port":8080}`), Result: json.RawMessage(`{"localPort":18080}`),
		IdempotencyKey: "management-task", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.ResourceSnapshots().Put(ctx, ResourceSnapshot{
		ID: uuid.NewString(), TaskID: taskID, Kind: "Service", Namespace: "default", Name: "example",
		Data: json.RawMessage(`{"spec":{"selector":{"app":"example"}}}`), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Idempotency().Reserve(ctx, IdempotencyRecord{
		Scope: "principal:" + principal.ID, Key: "management-request", RequestHash: "sha256:request",
		ResourceType: "task", ResourceID: taskID, Response: json.RawMessage(`{"accepted":true}`),
		CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Audit().Append(ctx, AuditEvent{
		ID: uuid.NewString(), PrincipalID: principal.ID, Action: "storage.seed", ResourceType: "storage",
		ResourceID: taskID, Outcome: "success", RequestID: "management-seed", Metadata: json.RawMessage(`{"safe":true}`),
		CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if retired, err := store.ManagementState().RetireBootstrap(ctx, 42, now); err != nil || !retired {
		t.Fatalf("retire management bootstrap = %t, error = %v", retired, err)
	}
	adminSessionID := sha256.Sum256([]byte("management-browser-session"))
	adminCSRF := sha256.Sum256([]byte("management-browser-csrf"))
	if err := store.AdminSessions().Create(ctx, AdminSession{
		IDHash: adminSessionID[:], PrincipalID: principal.ID, TokenFamilyID: familyID, AuthenticationType: "normal",
		CSRFTokenHash: adminCSRF[:], CreatedAt: now, LastSeenAt: now,
		IdleExpiresAt: now.Add(15 * time.Minute), AbsoluteExpiresAt: now.Add(8 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	stateHash := sha256.Sum256([]byte("management-auth-state"))
	if err := store.AuthTransactions().CreateAttempt(ctx, AuthAttempt{
		ID: uuid.NewString(), ProviderID: "corporate", StateHash: stateHash[:], ClientState: "client-state",
		ClientCallback: "http://127.0.0.1:49152/callback", Nonce: "nonce", PKCEChallenge: "challenge",
		UpstreamPKCEVerifier: "verifier", CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	codeHash := sha256.Sum256([]byte("management-exchange-code"))
	if err := store.AuthTransactions().CreateExchange(ctx, AuthExchange{
		CodeHash: codeHash[:], PrincipalID: principal.ID, ProviderID: "corporate", PKCEChallenge: "challenge",
		CreatedAt: now, ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	return managementSeed{principalID: principal.ID, sessionID: sessionID, taskID: taskID}
}
