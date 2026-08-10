package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const (
	ExportFormat        = "kubeloop-controller-storage"
	ExportFormatVersion = 1
	MaxExportBytes      = 1 << 30
	maxExportCellBytes  = 16 << 20
)

type ExportDocument struct {
	Format           string        `json:"format"`
	FormatVersion    int           `json:"formatVersion"`
	SchemaVersion    int           `json:"schemaVersion"`
	CreatedAt        time.Time     `json:"createdAt"`
	CreatedByVersion string        `json:"createdByVersion"`
	SourceBackend    Backend       `json:"sourceBackend"`
	Tables           []ExportTable `json:"tables"`
	ChecksumSHA256   string        `json:"checksumSha256,omitempty"`
}

type ExportTable struct {
	Name    string              `json:"name"`
	Columns []string            `json:"columns"`
	Rows    [][]json.RawMessage `json:"rows"`
}

type ExportMetadata struct {
	SchemaVersion    int       `json:"schemaVersion"`
	CreatedAt        time.Time `json:"createdAt"`
	CreatedByVersion string    `json:"createdByVersion"`
	SourceBackend    Backend   `json:"sourceBackend"`
	ChecksumSHA256   string    `json:"checksumSha256"`
	Rows             int       `json:"rows"`
}

type columnKind uint8

const (
	columnText columnKind = iota
	columnInteger
	columnBytes
	columnJSON
)

type columnSpec struct {
	name     string
	kind     columnKind
	nullable bool
}

type tableSpec struct {
	name     string
	columns  []columnSpec
	orderBy  []string
	omitRows bool
}

var exportTableSpecs = []tableSpec{
	{
		name: "principals",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "provider"},
			{name: "external_id"}, {name: "display_name"}, {name: "email"}, {name: "groups_json", kind: columnJSON},
			{name: "created_at"}, {name: "updated_at"},
		},
		orderBy: []string{"id"},
	},
	{
		name: "token_families",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "principal_id"},
			{name: "device_id"}, {name: "refresh_token_hash", kind: columnBytes}, {name: "created_at"},
			{name: "expires_at"}, {name: "revoked_at", nullable: true},
		},
		orderBy: []string{"id"},
	},
	{
		name: "refresh_tokens",
		columns: []columnSpec{
			{name: "token_hash", kind: columnBytes}, {name: "family_id"}, {name: "status"},
			{name: "created_at"}, {name: "used_at", nullable: true},
		},
		orderBy: []string{"token_hash"},
	},
	{
		name: "sessions",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "principal_id"},
			{name: "device_id"}, {name: "cluster_id"}, {name: "state"}, {name: "generation", kind: columnInteger},
			{name: "created_at"}, {name: "updated_at"}, {name: "expires_at"}, {name: "namespace"},
			{name: "last_heartbeat_at"}, {name: "network_spec_json", kind: columnJSON}, {name: "network_spec_hash"},
		},
		orderBy: []string{"id"},
	},
	{
		name: "tasks",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "principal_id"},
			{name: "session_id"}, {name: "type"}, {name: "state"}, {name: "spec_json", kind: columnJSON},
			{name: "result_json", kind: columnJSON, nullable: true}, {name: "idempotency_key"},
			{name: "created_at"}, {name: "updated_at"}, {name: "expires_at", nullable: true},
		},
		orderBy: []string{"id"},
	},
	{
		name: "resource_snapshots",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "task_id"},
			{name: "kind"}, {name: "namespace"}, {name: "name"}, {name: "data_json", kind: columnJSON},
			{name: "created_at"},
		},
		orderBy: []string{"id"},
	},
	{
		name: "idempotency_records",
		columns: []columnSpec{
			{name: "schema_version", kind: columnInteger}, {name: "scope"}, {name: "key"},
			{name: "request_hash"}, {name: "resource_type"}, {name: "resource_id"},
			{name: "response_json", kind: columnJSON, nullable: true}, {name: "created_at"}, {name: "expires_at"},
		},
		orderBy: []string{"scope", "key"},
	},
	{
		name: "audit_events",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "principal_id", nullable: true},
			{name: "action"}, {name: "resource_type"}, {name: "resource_id"}, {name: "outcome"},
			{name: "request_id"}, {name: "metadata_json", kind: columnJSON, nullable: true}, {name: "created_at"},
		},
		orderBy: []string{"created_at", "id"},
	},
	{
		name: "auth_attempts",
		columns: []columnSpec{
			{name: "id"}, {name: "schema_version", kind: columnInteger}, {name: "provider_id"},
			{name: "state_hash", kind: columnBytes}, {name: "client_state"}, {name: "client_callback"},
			{name: "nonce"}, {name: "pkce_challenge"}, {name: "upstream_pkce_verifier"},
			{name: "created_at"}, {name: "expires_at"},
		},
		orderBy:  []string{"id"},
		omitRows: true,
	},
	{
		name: "auth_exchanges",
		columns: []columnSpec{
			{name: "schema_version", kind: columnInteger}, {name: "code_hash", kind: columnBytes},
			{name: "principal_id"}, {name: "provider_id"}, {name: "pkce_challenge"},
			{name: "created_at"}, {name: "expires_at"},
		},
		orderBy:  []string{"code_hash"},
		omitRows: true,
	},
}

func decodeAndValidateExport(raw []byte) (ExportDocument, ExportMetadata, error) {
	if len(raw) == 0 {
		return ExportDocument{}, ExportMetadata{}, errors.New("storage export is empty")
	}
	if len(raw) > MaxExportBytes {
		return ExportDocument{}, ExportMetadata{}, errors.New("storage export exceeds the size limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var document ExportDocument
	if err := decoder.Decode(&document); err != nil {
		return ExportDocument{}, ExportMetadata{}, errors.New("decode storage export")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return ExportDocument{}, ExportMetadata{}, err
	}
	if err := validateExportDocument(&document); err != nil {
		return ExportDocument{}, ExportMetadata{}, err
	}
	return document, exportMetadata(document), nil
}

func validateExportDocument(document *ExportDocument) error {
	if document.Format != ExportFormat || document.FormatVersion != ExportFormatVersion {
		return errors.New("unsupported storage export format")
	}
	if document.SchemaVersion != currentSchemaVersion() {
		return fmt.Errorf(
			"storage export schema version %d does not match supported version %d",
			document.SchemaVersion,
			currentSchemaVersion(),
		)
	}
	if document.CreatedAt.IsZero() || document.CreatedAt.Location() != time.UTC {
		return errors.New("storage export creation time must be UTC")
	}
	if createdByVersion := strings.TrimSpace(document.CreatedByVersion); createdByVersion == "" || len(createdByVersion) > 256 {
		return errors.New("storage export creation version is invalid")
	}
	if document.SourceBackend != BackendSQLite && document.SourceBackend != BackendPostgreSQL {
		return errors.New("storage export source backend is invalid")
	}
	if len(document.Tables) != len(exportTableSpecs) {
		return errors.New("storage export table inventory is incomplete")
	}
	for index, spec := range exportTableSpecs {
		table := document.Tables[index]
		if table.Name != spec.name {
			return fmt.Errorf("storage export table %d must be %q", index, spec.name)
		}
		if len(table.Columns) != len(spec.columns) {
			return fmt.Errorf("storage export table %q has an invalid column inventory", spec.name)
		}
		for columnIndex, column := range spec.columns {
			if table.Columns[columnIndex] != column.name {
				return fmt.Errorf("storage export table %q has an invalid column inventory", spec.name)
			}
		}
		for rowIndex, row := range table.Rows {
			if spec.omitRows {
				return fmt.Errorf("storage export table %q must not contain transient authentication data", spec.name)
			}
			if len(row) != len(spec.columns) {
				return fmt.Errorf("storage export table %q row %d has an invalid column count", spec.name, rowIndex)
			}
			for columnIndex, value := range row {
				if err := validateExportValue(value, spec.columns[columnIndex]); err != nil {
					return fmt.Errorf(
						"storage export table %q row %d column %q: %w",
						spec.name, rowIndex, spec.columns[columnIndex].name, err,
					)
				}
			}
			if err := validateExportDomainRow(spec, row); err != nil {
				return fmt.Errorf("storage export table %q row %d: %w", spec.name, rowIndex, err)
			}
		}
	}
	wantChecksum := document.ChecksumSHA256
	if len(wantChecksum) != sha256.Size*2 || strings.ToLower(wantChecksum) != wantChecksum {
		return errors.New("storage export checksum is invalid")
	}
	if _, err := hex.DecodeString(wantChecksum); err != nil {
		return errors.New("storage export checksum is invalid")
	}
	document.ChecksumSHA256 = ""
	payload, err := json.Marshal(document)
	document.ChecksumSHA256 = wantChecksum
	if err != nil {
		return errors.New("encode storage export checksum payload")
	}
	actual := sha256.Sum256(payload)
	if !bytes.Equal(actual[:], mustDecodeHex(wantChecksum)) {
		return errors.New("storage export checksum mismatch")
	}
	return nil
}

func validateExportValue(raw json.RawMessage, spec columnSpec) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if !spec.nullable {
			return errors.New("value must not be null")
		}
		return nil
	}
	if len(raw) > maxExportCellBytes {
		return errors.New("value exceeds the size limit")
	}
	switch spec.kind {
	case columnText:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("value must be text")
		}
	case columnInteger:
		var value int64
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("value must be an integer")
		}
	case columnBytes:
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return errors.New("value must be base64 text")
		}
		if _, err := base64.StdEncoding.DecodeString(value); err != nil {
			return errors.New("value must be base64 text")
		}
	case columnJSON:
		if !json.Valid(raw) {
			return errors.New("value must contain JSON")
		}
	default:
		return errors.New("value has an unsupported type")
	}
	return nil
}

func marshalExport(document ExportDocument) ([]byte, ExportMetadata, error) {
	document.ChecksumSHA256 = ""
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, ExportMetadata{}, errors.New("encode storage export checksum payload")
	}
	checksum := sha256.Sum256(payload)
	document.ChecksumSHA256 = hex.EncodeToString(checksum[:])
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, ExportMetadata{}, errors.New("encode storage export")
	}
	encoded = append(encoded, '\n')
	if len(encoded) > MaxExportBytes {
		return nil, ExportMetadata{}, errors.New("storage export exceeds the size limit")
	}
	return encoded, exportMetadata(document), nil
}

func exportMetadata(document ExportDocument) ExportMetadata {
	rows := 0
	for _, table := range document.Tables {
		rows += len(table.Rows)
	}
	return ExportMetadata{
		SchemaVersion: document.SchemaVersion, CreatedAt: document.CreatedAt, CreatedByVersion: document.CreatedByVersion,
		SourceBackend: document.SourceBackend, ChecksumSHA256: document.ChecksumSHA256, Rows: rows,
	}
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("storage export contains trailing data")
	}
	return nil
}

func mustDecodeHex(value string) []byte {
	decoded, _ := hex.DecodeString(value)
	return decoded
}
