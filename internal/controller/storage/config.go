package storage

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type Backend string

const (
	BackendSQLite     Backend = "sqlite"
	BackendPostgreSQL Backend = "postgresql"

	DefaultSQLitePath                        = "kubeloop.db"
	DefaultBusyTimeout                       = 5 * time.Second
	DefaultConnectTimeout                    = 10 * time.Second
	DefaultQueryTimeout                      = 5 * time.Second
	DefaultPostgreSQLMaxOpen                 = 20
	DefaultPostgreSQLMaxIdle                 = 5
	DefaultPostgreSQLTransactionMaxRetries   = 3
	DefaultPostgreSQLTransactionRetryBackoff = 25 * time.Millisecond
)

type Config struct {
	Backend                 Backend
	SQLitePath              string
	PostgreSQLDSN           string
	ControllerReplicas      int
	BusyTimeout             time.Duration
	ConnectTimeout          time.Duration
	QueryTimeout            time.Duration
	MaxOpenConnections      int
	MaxIdleConnections      int
	ConnectionMaxLifetime   time.Duration
	TransactionMaxRetries   int
	TransactionRetryBackoff time.Duration
	AllowInsecurePostgreSQL bool
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		Backend:            Backend(strings.ToLower(strings.TrimSpace(os.Getenv("KUBELOOP_STORAGE_TYPE")))),
		SQLitePath:         strings.TrimSpace(os.Getenv("KUBELOOP_SQLITE_PATH")),
		PostgreSQLDSN:      strings.TrimSpace(os.Getenv("KUBELOOP_POSTGRESQL_DSN")),
		ControllerReplicas: 1,
	}
	if raw := strings.TrimSpace(os.Getenv("KUBELOOP_CONTROLLER_REPLICAS")); raw != "" {
		replicas, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("parse KUBELOOP_CONTROLLER_REPLICAS: %w", err)
		}
		config.ControllerReplicas = replicas
	}
	if file := strings.TrimSpace(os.Getenv("KUBELOOP_POSTGRESQL_DSN_FILE")); file != "" {
		if config.PostgreSQLDSN != "" {
			return Config{}, errors.New("configure only one of KUBELOOP_POSTGRESQL_DSN and KUBELOOP_POSTGRESQL_DSN_FILE")
		}
		content, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			return Config{}, fmt.Errorf("read PostgreSQL DSN file: %w", err)
		}
		config.PostgreSQLDSN = strings.TrimSpace(string(content))
	}
	if err := applyDurationEnv("KUBELOOP_POSTGRESQL_CONNECT_TIMEOUT", &config.ConnectTimeout); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv("KUBELOOP_POSTGRESQL_QUERY_TIMEOUT", &config.QueryTimeout); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv("KUBELOOP_POSTGRESQL_MAX_OPEN_CONNECTIONS", &config.MaxOpenConnections); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv("KUBELOOP_POSTGRESQL_MAX_IDLE_CONNECTIONS", &config.MaxIdleConnections); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv("KUBELOOP_POSTGRESQL_CONNECTION_MAX_LIFETIME", &config.ConnectionMaxLifetime); err != nil {
		return Config{}, err
	}
	if err := applyIntEnv("KUBELOOP_POSTGRESQL_TRANSACTION_MAX_RETRIES", &config.TransactionMaxRetries); err != nil {
		return Config{}, err
	}
	if err := applyDurationEnv("KUBELOOP_POSTGRESQL_TRANSACTION_RETRY_BACKOFF", &config.TransactionRetryBackoff); err != nil {
		return Config{}, err
	}
	return config.normalized()
}

func applyDurationEnv(name string, destination *time.Duration) error {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	*destination = value
	return nil
}

func applyIntEnv(name string, destination *int) error {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", name, err)
	}
	*destination = value
	return nil
}

func (config Config) normalized() (Config, error) {
	if config.Backend == "" {
		config.Backend = BackendSQLite
	}
	if config.ControllerReplicas < 0 {
		return Config{}, errors.New("Controller replicas must not be negative")
	}
	if config.ControllerReplicas == 0 {
		config.ControllerReplicas = 1
	}
	if config.BusyTimeout < 0 || config.ConnectTimeout < 0 || config.QueryTimeout < 0 || config.ConnectionMaxLifetime < 0 {
		return Config{}, errors.New("storage timeouts and connection lifetime must not be negative")
	}
	if config.BusyTimeout == 0 {
		config.BusyTimeout = DefaultBusyTimeout
	}
	if config.ConnectTimeout == 0 {
		config.ConnectTimeout = DefaultConnectTimeout
	}
	if config.QueryTimeout == 0 {
		config.QueryTimeout = DefaultQueryTimeout
	}
	if config.ConnectionMaxLifetime == 0 {
		config.ConnectionMaxLifetime = 30 * time.Minute
	}
	switch config.Backend {
	case BackendSQLite:
		if config.ControllerReplicas != 1 {
			return Config{}, errors.New("SQLite storage requires exactly one Controller replica")
		}
		if strings.TrimSpace(config.SQLitePath) == "" {
			config.SQLitePath = DefaultSQLitePath
		}
		config.SQLitePath = filepath.Clean(config.SQLitePath)
		config.MaxOpenConnections = 1
		config.MaxIdleConnections = 1
	case BackendPostgreSQL:
		if strings.TrimSpace(config.PostgreSQLDSN) == "" {
			return Config{}, errors.New("PostgreSQL DSN is required")
		}
		parsed, err := pgx.ParseConfig(config.PostgreSQLDSN)
		if err != nil {
			return Config{}, errors.New("PostgreSQL DSN is invalid")
		}
		if !postgreSQLTLSRequired(parsed) && !config.AllowInsecurePostgreSQL {
			return Config{}, errors.New("PostgreSQL TLS is required; set sslmode=require or stronger")
		}
		if config.MaxOpenConnections < 0 {
			return Config{}, errors.New("PostgreSQL max open connections must not be negative")
		}
		if config.MaxOpenConnections == 0 {
			config.MaxOpenConnections = DefaultPostgreSQLMaxOpen
		}
		if config.MaxIdleConnections < 0 {
			return Config{}, errors.New("PostgreSQL max idle connections must not be negative")
		}
		if config.MaxIdleConnections == 0 {
			config.MaxIdleConnections = min(DefaultPostgreSQLMaxIdle, config.MaxOpenConnections)
		}
		if config.MaxIdleConnections > config.MaxOpenConnections {
			return Config{}, errors.New("PostgreSQL max idle connections must not exceed max open connections")
		}
		if config.TransactionMaxRetries < 0 || config.TransactionMaxRetries > 10 {
			return Config{}, errors.New("PostgreSQL transaction max retries must be between 0 and 10")
		}
		if config.TransactionMaxRetries == 0 {
			config.TransactionMaxRetries = DefaultPostgreSQLTransactionMaxRetries
		}
		if config.TransactionRetryBackoff < 0 || config.TransactionRetryBackoff > time.Second {
			return Config{}, errors.New("PostgreSQL transaction retry backoff must be between 0 and 1s")
		}
		if config.TransactionRetryBackoff == 0 {
			config.TransactionRetryBackoff = DefaultPostgreSQLTransactionRetryBackoff
		}
	default:
		return Config{}, fmt.Errorf("unsupported storage backend %q", config.Backend)
	}
	return config, nil
}

func postgreSQLTLSRequired(config *pgx.ConnConfig) bool {
	if config == nil || config.TLSConfig == nil {
		return false
	}
	for _, fallback := range config.Fallbacks {
		if fallback == nil || fallback.TLSConfig == nil {
			return false
		}
	}
	return true
}

func RedactedPostgreSQLDSN(raw string) string {
	parsed, err := url.Parse(raw)
	if err == nil && parsed.IsAbs() && (parsed.Scheme == "postgres" || parsed.Scheme == "postgresql") {
		if parsed.User != nil {
			username := parsed.User.Username()
			parsed.User = url.UserPassword(username, "REDACTED")
		}
		query := parsed.Query()
		for key := range query {
			lower := strings.ToLower(key)
			if lower == "password" || lower == "passfile" {
				query.Set(key, "REDACTED")
			}
		}
		parsed.RawQuery = query.Encode()
		return parsed.String()
	}
	// Keyword/value DSNs allow quoted values containing whitespace and escape
	// sequences. Returning a reconstructed subset is easy to get wrong, so hide
	// the entire non-URL DSN instead of risking a partial credential disclosure.
	return "[REDACTED PostgreSQL DSN]"
}
