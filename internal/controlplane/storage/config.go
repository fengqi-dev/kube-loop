package storage

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

type Backend string

const (
	BackendSQLite     Backend = "sqlite"
	BackendPostgreSQL Backend = "postgresql"
	BackendMySQL      Backend = "mysql"

	DefaultSQLitePath              = "kubeloop.db"
	DefaultBusyTimeout             = 5 * time.Second
	DefaultConnectTimeout          = 10 * time.Second
	DefaultQueryTimeout            = 5 * time.Second
	DefaultMaxOpen                 = 20
	DefaultMaxIdle                 = 5
	DefaultTransactionMaxRetries   = 3
	DefaultTransactionRetryBackoff = 25 * time.Millisecond
)

type Config struct {
	Backend                 Backend
	SQLitePath              string
	DatasourceURL           string
	ControlPlaneReplicas    int
	BusyTimeout             time.Duration
	ConnectTimeout          time.Duration
	QueryTimeout            time.Duration
	MaxOpenConnections      int
	MaxIdleConnections      int
	ConnectionMaxLifetime   time.Duration
	TransactionMaxRetries   int
	TransactionRetryBackoff time.Duration
	AllowInsecureDatasource bool
}

// Normalize validates and applies defaults to Control Plane storage settings.
func Normalize(config Config) (Config, error) {
	config.DatasourceURL = strings.TrimSpace(config.DatasourceURL)
	backend, err := datasourceBackend(config.DatasourceURL)
	if err != nil {
		return Config{}, err
	}
	if config.Backend != "" && config.Backend != backend {
		return Config{}, errors.New(
			"storage backend conflicts with datasource URL",
		)
	}
	config.Backend = backend
	if config.ControlPlaneReplicas < 0 {
		return Config{}, errors.New(
			"control Plane replicas must not be negative",
		)
	}
	if config.ControlPlaneReplicas == 0 {
		config.ControlPlaneReplicas = 1
	}
	if config.BusyTimeout < 0 || config.ConnectTimeout < 0 ||
		config.QueryTimeout < 0 ||
		config.ConnectionMaxLifetime < 0 {
		return Config{}, errors.New(
			"storage timeouts and connection lifetime must not be negative",
		)
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
	if config.Backend == BackendSQLite {
		if config.ControlPlaneReplicas != 1 {
			return Config{}, errors.New(
				"sQLite storage requires exactly one Control Plane replica",
			)
		}
		if strings.TrimSpace(config.SQLitePath) == "" {
			config.SQLitePath = DefaultSQLitePath
		}
		config.SQLitePath = filepath.Clean(config.SQLitePath)
		config.MaxOpenConnections = 1
		config.MaxIdleConnections = 1
		return config, nil
	}
	if err := validateDatasource(config); err != nil {
		return Config{}, err
	}
	if config.MaxOpenConnections < 0 {
		return Config{}, errors.New(
			"datasource max open connections must not be negative",
		)
	}
	if config.MaxOpenConnections == 0 {
		config.MaxOpenConnections = DefaultMaxOpen
	}
	if config.MaxIdleConnections < 0 {
		return Config{}, errors.New(
			"datasource max idle connections must not be negative",
		)
	}
	if config.MaxIdleConnections == 0 {
		config.MaxIdleConnections = min(
			DefaultMaxIdle,
			config.MaxOpenConnections,
		)
	}
	if config.MaxIdleConnections > config.MaxOpenConnections {
		return Config{}, errors.New(
			"datasource max idle connections must not exceed max open connections",
		)
	}
	if config.TransactionMaxRetries < 0 || config.TransactionMaxRetries > 10 {
		return Config{}, errors.New(
			"datasource transaction max retries must be between 0 and 10",
		)
	}
	if config.TransactionMaxRetries == 0 {
		config.TransactionMaxRetries = DefaultTransactionMaxRetries
	}
	if config.TransactionRetryBackoff < 0 ||
		config.TransactionRetryBackoff > time.Second {
		return Config{}, errors.New(
			"datasource transaction retry backoff must be between 0 and 1s",
		)
	}
	if config.TransactionRetryBackoff == 0 {
		config.TransactionRetryBackoff = DefaultTransactionRetryBackoff
	}
	return config, nil
}

func datasourceBackend(raw string) (Backend, error) {
	if raw == "" {
		return BackendSQLite, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() {
		return "", errors.New("datasource URL is invalid")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "postgres", "postgresql":
		return BackendPostgreSQL, nil
	case string(BackendMySQL):
		return BackendMySQL, nil
	default:
		return "", errors.New("datasource URL must use postgresql or mysql")
	}
}

func validateDatasource(config Config) error {
	switch config.Backend {
	case BackendSQLite:
		return nil
	case BackendPostgreSQL:
		parsed, err := pgx.ParseConfig(config.DatasourceURL)
		if err != nil {
			return errors.New("postgreSQL datasource URL is invalid")
		}
		if !postgreSQLTLSRequired(parsed) && !config.AllowInsecureDatasource {
			return errors.New(
				"postgreSQL datasource TLS is required; set sslmode=require or stronger",
			)
		}
	case BackendMySQL:
		parsed, err := parseMySQLURL(config.DatasourceURL)
		if err != nil {
			return err
		}
		if !mysqlTLSRequired(parsed.TLSConfig) &&
			!config.AllowInsecureDatasource {
			return errors.New("mySQL datasource TLS is required; set tls=true")
		}
	}
	return nil
}

func (config Config) normalized() (Config, error) { return Normalize(config) }

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

func mysqlTLSRequired(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return value != "" && value != "false" && value != "preferred" &&
		value != "skip-verify"
}

func parseMySQLURL(raw string) (*mysql.Config, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != string(BackendMySQL) ||
		parsed.Host == "" ||
		strings.TrimPrefix(parsed.Path, "/") == "" {
		return nil, errors.New("mySQL datasource URL is invalid")
	}
	config := mysql.NewConfig()
	config.Net = "tcp"
	config.ClientFoundRows = true
	config.Addr = parsed.Host
	config.DBName = strings.TrimPrefix(parsed.Path, "/")
	if parsed.User != nil {
		config.User = parsed.User.Username()
		config.Passwd, _ = parsed.User.Password()
	}
	config.Params = make(map[string]string)
	for key, values := range parsed.Query() {
		if len(values) != 1 {
			return nil, errors.New(
				"mySQL datasource URL contains duplicate parameters",
			)
		}
		switch key {
		case "tls":
			config.TLSConfig = values[0]
		case "charset", "collation", "time_zone":
			config.Params[key] = values[0]
		default:
			return nil, fmt.Errorf(
				"mySQL datasource URL parameter %q is not supported",
				key,
			)
		}
	}
	return config, nil
}

func RedactedDatasourceURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() {
		return "[REDACTED datasource URL]"
	}
	if parsed.User != nil {
		parsed.User = url.UserPassword(parsed.User.Username(), "REDACTED")
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
