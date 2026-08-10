package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type ExportOptions struct {
	CreatedByVersion string
	Now              func() time.Time
}

type ImportOptions struct {
	ConfirmedEmpty bool
	ImportedBy     string
	Now            func() time.Time
}

type ImportResult struct {
	SchemaVersion  int       `json:"schemaVersion"`
	ImportedAt     time.Time `json:"importedAt"`
	ImportedBy     string    `json:"importedBy"`
	ChecksumSHA256 string    `json:"checksumSha256"`
	Rows           int       `json:"rows"`
	AuditEventID   string    `json:"auditEventId"`
}

type BackupResult struct {
	Path           string    `json:"path"`
	SchemaVersion  int       `json:"schemaVersion"`
	CreatedAt      time.Time `json:"createdAt"`
	ChecksumSHA256 string    `json:"checksumSha256"`
	Bytes          int64     `json:"bytes"`
}

// Export creates a deterministic, checksummed logical export from a single
// database snapshot. It never serializes storage configuration or provider
// configuration, so database, OIDC, and AD secrets are outside the format.
func Export(ctx context.Context, rawConfig Config, options ExportOptions) ([]byte, ExportMetadata, error) {
	createdByVersion := strings.TrimSpace(options.CreatedByVersion)
	if createdByVersion == "" || len(createdByVersion) > 256 {
		return nil, ExportMetadata{}, errors.New("storage export creation version is required")
	}
	config, err := rawConfig.normalized()
	if err != nil {
		return nil, ExportMetadata{}, err
	}
	if config.Backend == BackendSQLite {
		if err := requireExistingSQLite(config.SQLitePath); err != nil {
			return nil, ExportMetadata{}, err
		}
	}
	store, err := Open(ctx, config)
	if err != nil {
		return nil, ExportMetadata{}, err
	}
	defer store.Close()

	txOptions := (*sql.TxOptions)(nil)
	if store.backend == BackendPostgreSQL {
		txOptions = &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}
	}
	transaction, err := store.db.BeginTx(ctx, txOptions)
	if err != nil {
		return nil, ExportMetadata{}, databaseError("begin storage export snapshot", err)
	}
	defer transaction.Rollback()

	var schemaVersion int
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return nil, ExportMetadata{}, databaseError("read storage export schema version", err)
	}
	if schemaVersion != currentSchemaVersion() {
		return nil, ExportMetadata{}, fmt.Errorf(
			"storage schema version %d does not match supported version %d",
			schemaVersion,
			currentSchemaVersion(),
		)
	}
	document := ExportDocument{
		Format: ExportFormat, FormatVersion: ExportFormatVersion, SchemaVersion: schemaVersion,
		CreatedAt: managementNow(options.Now), CreatedByVersion: createdByVersion, SourceBackend: store.backend,
		Tables: make([]ExportTable, 0, len(exportTableSpecs)),
	}
	for _, spec := range exportTableSpecs {
		table, exportErr := exportTable(ctx, transaction, spec)
		if exportErr != nil {
			return nil, ExportMetadata{}, exportErr
		}
		document.Tables = append(document.Tables, table)
	}
	if err := transaction.Commit(); err != nil {
		return nil, ExportMetadata{}, databaseError("finish storage export snapshot", err)
	}
	return marshalExport(document)
}

// ValidateExport performs every format, schema, inventory, cell-type, and
// checksum check used before Import opens its destination database.
func ValidateExport(raw []byte) (ExportMetadata, error) {
	_, metadata, err := decodeAndValidateExport(raw)
	return metadata, err
}

// Import restores a validated logical export into an empty PostgreSQL schema.
// All tables and the success audit record are written in one transaction.
func Import(ctx context.Context, rawConfig Config, raw []byte, options ImportOptions) (ImportResult, error) {
	if !options.ConfirmedEmpty {
		return ImportResult{}, errors.New("storage import requires explicit empty-database confirmation")
	}
	importedBy := strings.TrimSpace(options.ImportedBy)
	if importedBy == "" || len(importedBy) > 256 {
		return ImportResult{}, errors.New("storage import actor is required")
	}
	document, metadata, err := decodeAndValidateExport(raw)
	if err != nil {
		return ImportResult{}, err
	}
	config, err := rawConfig.normalized()
	if err != nil {
		return ImportResult{}, err
	}
	if config.Backend != BackendPostgreSQL {
		return ImportResult{}, errors.New("logical storage import requires an empty PostgreSQL backend")
	}
	store, err := Open(ctx, config)
	if err != nil {
		return ImportResult{}, err
	}
	defer store.Close()
	return store.importDocument(ctx, document, metadata, importedBy, managementNow(options.Now))
}

func (store *Store) importDocument(
	ctx context.Context,
	document ExportDocument,
	metadata ExportMetadata,
	importedBy string,
	importedAt time.Time,
) (ImportResult, error) {
	transaction, err := store.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ImportResult{}, databaseError("begin storage import", err)
	}
	defer transaction.Rollback()

	tableNames := make([]string, 0, len(exportTableSpecs))
	for _, spec := range exportTableSpecs {
		tableNames = append(tableNames, spec.name)
	}
	if _, err := transaction.ExecContext(ctx, `LOCK TABLE `+strings.Join(tableNames, ", ")+` IN ACCESS EXCLUSIVE MODE`); err != nil {
		return ImportResult{}, databaseError("lock storage for import", err)
	}
	var targetSchemaVersion int
	if err := transaction.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&targetSchemaVersion); err != nil {
		return ImportResult{}, databaseError("read import target schema version", err)
	}
	if targetSchemaVersion != document.SchemaVersion {
		return ImportResult{}, fmt.Errorf(
			"import target schema version %d does not match export version %d",
			targetSchemaVersion,
			document.SchemaVersion,
		)
	}
	for _, spec := range exportTableSpecs {
		var count int64
		if err := transaction.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+spec.name).Scan(&count); err != nil {
			return ImportResult{}, databaseError("check import target emptiness", err)
		}
		if count != 0 {
			return ImportResult{}, fmt.Errorf("storage import target is not empty: table %q contains data", spec.name)
		}
	}
	for index, spec := range exportTableSpecs {
		if err := importTable(ctx, transaction, spec, document.Tables[index]); err != nil {
			return ImportResult{}, err
		}
	}

	auditEventID := uuid.NewString()
	metadataJSON, err := json.Marshal(map[string]any{
		"checksumSha256":   metadata.ChecksumSHA256,
		"createdAt":        metadata.CreatedAt,
		"createdByVersion": metadata.CreatedByVersion,
		"importedBy":       importedBy,
		"rows":             metadata.Rows,
		"sourceBackend":    metadata.SourceBackend,
	})
	if err != nil {
		return ImportResult{}, errors.New("encode storage import audit metadata")
	}
	insertAudit := repositoryBase{backend: BackendPostgreSQL}.bind(`INSERT INTO audit_events (
		id, schema_version, principal_id, action, resource_type, resource_id,
		outcome, request_id, metadata_json, created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if _, err := transaction.ExecContext(
		ctx,
		insertAudit,
		auditEventID,
		ObjectSchemaVersion,
		nil,
		"storage.import",
		"storage",
		metadata.ChecksumSHA256,
		"success",
		"storage-import-"+auditEventID,
		string(metadataJSON),
		formatTime(importedAt),
	); err != nil {
		return ImportResult{}, databaseError("append storage import audit event", err)
	}
	if err := transaction.Commit(); err != nil {
		return ImportResult{}, databaseError("commit storage import", err)
	}
	return ImportResult{
		SchemaVersion: document.SchemaVersion, ImportedAt: importedAt, ImportedBy: importedBy,
		ChecksumSHA256: metadata.ChecksumSHA256, Rows: metadata.Rows, AuditEventID: auditEventID,
	}, nil
}

// BackupSQLite creates a compact, transactionally consistent SQLite snapshot
// with VACUUM INTO, validates it, and publishes it by same-directory rename.
func BackupSQLite(ctx context.Context, rawConfig Config, destination string, now func() time.Time) (BackupResult, error) {
	config, err := rawConfig.normalized()
	if err != nil {
		return BackupResult{}, err
	}
	if config.Backend != BackendSQLite {
		return BackupResult{}, errors.New("physical storage backup is supported only for SQLite")
	}
	if err := requireExistingSQLite(config.SQLitePath); err != nil {
		return BackupResult{}, err
	}
	source, err := filepath.Abs(config.SQLitePath)
	if err != nil {
		return BackupResult{}, errors.New("resolve SQLite source path")
	}
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return BackupResult{}, errors.New("SQLite backup destination is required")
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return BackupResult{}, errors.New("resolve SQLite backup destination")
	}
	if destination == source {
		return BackupResult{}, errors.New("SQLite backup destination must differ from the source")
	}
	if _, err := os.Lstat(destination); err == nil {
		return BackupResult{}, errors.New("SQLite backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return BackupResult{}, errors.New("inspect SQLite backup destination")
	}
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return BackupResult{}, errors.New("create SQLite backup directory")
	}
	if info, err := os.Lstat(directory); err != nil {
		return BackupResult{}, errors.New("inspect SQLite backup directory")
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return BackupResult{}, errors.New("SQLite backup directory must be a real directory")
	}
	temporary, err := os.CreateTemp(directory, "."+filepath.Base(destination)+".tmp-*")
	if err != nil {
		return BackupResult{}, errors.New("create SQLite backup temporary path")
	}
	temporaryPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(temporaryPath)
		return BackupResult{}, errors.New("close SQLite backup temporary path")
	}
	if err := os.Remove(temporaryPath); err != nil {
		return BackupResult{}, errors.New("prepare SQLite backup temporary path")
	}
	defer os.Remove(temporaryPath)

	store, err := Open(ctx, config)
	if err != nil {
		return BackupResult{}, err
	}
	if _, err := store.db.ExecContext(ctx, `VACUUM INTO ?`, temporaryPath); err != nil {
		_ = store.Close()
		return BackupResult{}, databaseError("create consistent SQLite backup", err)
	}
	if err := store.Close(); err != nil {
		return BackupResult{}, errors.New("close SQLite source after backup")
	}
	schemaVersion, err := validateSQLiteBackup(ctx, temporaryPath)
	if err != nil {
		return BackupResult{}, err
	}
	checksum, size, err := checksumFile(temporaryPath)
	if err != nil {
		return BackupResult{}, err
	}
	if err := os.Chmod(temporaryPath, 0o600); err != nil {
		return BackupResult{}, errors.New("set SQLite backup permissions")
	}
	file, err := os.OpenFile(temporaryPath, os.O_RDWR, 0)
	if err != nil {
		return BackupResult{}, errors.New("open SQLite backup for sync")
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return BackupResult{}, errors.New("sync SQLite backup")
	}
	if err := file.Close(); err != nil {
		return BackupResult{}, errors.New("close SQLite backup")
	}
	if _, err := os.Lstat(destination); err == nil {
		return BackupResult{}, errors.New("SQLite backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return BackupResult{}, errors.New("inspect SQLite backup destination")
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		return BackupResult{}, errors.New("publish SQLite backup")
	}
	if runtime.GOOS != "windows" {
		if directoryHandle, openErr := os.Open(directory); openErr == nil {
			_ = directoryHandle.Sync()
			_ = directoryHandle.Close()
		}
	}
	return BackupResult{
		Path: destination, SchemaVersion: schemaVersion, CreatedAt: managementNow(now),
		ChecksumSHA256: checksum, Bytes: size,
	}, nil
}

func exportTable(ctx context.Context, transaction *sql.Tx, spec tableSpec) (ExportTable, error) {
	columnNames := make([]string, len(spec.columns))
	for index, column := range spec.columns {
		columnNames[index] = column.name
	}
	if spec.omitRows {
		return ExportTable{Name: spec.name, Columns: columnNames, Rows: make([][]json.RawMessage, 0)}, nil
	}
	query := `SELECT ` + strings.Join(columnNames, ", ") + ` FROM ` + spec.name + ` ORDER BY ` + strings.Join(spec.orderBy, ", ")
	rows, err := transaction.QueryContext(ctx, query)
	if err != nil {
		return ExportTable{}, databaseError("read storage export table "+spec.name, err)
	}
	defer rows.Close()
	table := ExportTable{Name: spec.name, Columns: columnNames, Rows: make([][]json.RawMessage, 0)}
	for rows.Next() {
		values := make([]any, len(spec.columns))
		destinations := make([]any, len(values))
		for index := range values {
			destinations[index] = &values[index]
		}
		if err := rows.Scan(destinations...); err != nil {
			return ExportTable{}, databaseError("decode storage export table "+spec.name, err)
		}
		row := make([]json.RawMessage, len(values))
		for index, value := range values {
			encoded, err := encodeDatabaseValue(value, spec.columns[index])
			if err != nil {
				return ExportTable{}, fmt.Errorf("encode storage export table %q column %q: %w", spec.name, spec.columns[index].name, err)
			}
			row[index] = encoded
		}
		table.Rows = append(table.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return ExportTable{}, databaseError("read storage export table "+spec.name, err)
	}
	return table, nil
}

func encodeDatabaseValue(value any, spec columnSpec) (json.RawMessage, error) {
	if value == nil {
		if !spec.nullable {
			return nil, errors.New("database returned null for a required value")
		}
		return json.RawMessage("null"), nil
	}
	switch spec.kind {
	case columnText:
		text, err := databaseText(value)
		if err != nil {
			return nil, err
		}
		return json.Marshal(text)
	case columnInteger:
		switch typed := value.(type) {
		case int64:
			return json.Marshal(typed)
		case int32:
			return json.Marshal(int64(typed))
		case int:
			return json.Marshal(int64(typed))
		default:
			return nil, errors.New("database returned a non-integer value")
		}
	case columnBytes:
		binary, ok := value.([]byte)
		if !ok {
			return nil, errors.New("database returned a non-binary value")
		}
		return json.Marshal(base64.StdEncoding.EncodeToString(binary))
	case columnJSON:
		text, err := databaseText(value)
		if err != nil {
			return nil, err
		}
		if !json.Valid([]byte(text)) {
			return nil, errors.New("database returned invalid JSON")
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, []byte(text)); err != nil {
			return nil, errors.New("compact database JSON")
		}
		return json.RawMessage(compact.String()), nil
	default:
		return nil, errors.New("unsupported export column type")
	}
}

func databaseText(value any) (string, error) {
	switch typed := value.(type) {
	case string:
		return typed, nil
	case []byte:
		return string(typed), nil
	default:
		return "", errors.New("database returned a non-text value")
	}
}

func importTable(ctx context.Context, transaction *sql.Tx, spec tableSpec, table ExportTable) error {
	columnNames := make([]string, len(spec.columns))
	placeholders := make([]string, len(spec.columns))
	for index, column := range spec.columns {
		columnNames[index] = column.name
		placeholders[index] = "?"
	}
	query := repositoryBase{backend: BackendPostgreSQL}.bind(
		`INSERT INTO ` + spec.name + ` (` + strings.Join(columnNames, ", ") + `) VALUES (` + strings.Join(placeholders, ", ") + `)`,
	)
	for rowIndex, row := range table.Rows {
		arguments := make([]any, len(row))
		for columnIndex, raw := range row {
			value, err := decodeExportValue(raw, spec.columns[columnIndex])
			if err != nil {
				return fmt.Errorf("decode storage import table %q row %d: %w", spec.name, rowIndex, err)
			}
			arguments[columnIndex] = value
		}
		if _, err := transaction.ExecContext(ctx, query, arguments...); err != nil {
			return databaseError("write storage import table "+spec.name, err)
		}
	}
	return nil
}

func decodeExportValue(raw json.RawMessage, spec columnSpec) (any, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	switch spec.kind {
	case columnText:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return value, nil
	case columnInteger:
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return value, nil
	case columnBytes:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		return base64.StdEncoding.DecodeString(value)
	case columnJSON:
		return string(raw), nil
	default:
		return nil, errors.New("unsupported import column type")
	}
}

func validateSQLiteBackup(ctx context.Context, path string) (int, error) {
	location := (&url.URL{Scheme: "file", Path: path}).String() + "?mode=ro"
	database, err := sql.Open("sqlite", location)
	if err != nil {
		return 0, errors.New("open SQLite backup for validation")
	}
	defer database.Close()
	var integrity string
	if err := database.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&integrity); err != nil || !strings.EqualFold(integrity, "ok") {
		return 0, errors.New("SQLite backup integrity validation failed")
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schemaVersion); err != nil {
		return 0, errors.New("read SQLite backup schema version")
	}
	if schemaVersion != currentSchemaVersion() {
		return 0, fmt.Errorf(
			"SQLite backup schema version %d does not match supported version %d",
			schemaVersion,
			currentSchemaVersion(),
		)
	}
	return schemaVersion, nil
}

func checksumFile(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, errors.New("open SQLite backup for checksum")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", 0, errors.New("inspect SQLite backup")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", 0, errors.New("checksum SQLite backup")
	}
	return hex.EncodeToString(hash.Sum(nil)), info.Size(), nil
}

func requireExistingSQLite(path string) error {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return errors.New("resolve SQLite source path")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return errors.New("SQLite source database does not exist")
		}
		return errors.New("inspect SQLite source database")
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("SQLite source database must be a regular file")
	}
	return nil
}

func managementNow(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}
	return now().UTC()
}
