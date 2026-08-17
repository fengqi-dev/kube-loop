package storage

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	env "github.com/Netflix/go-env"
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

type storageEnvironment struct {
	SQLitePath              string        `env:"KUBELOOP_SQLITE_PATH"`
	DatasourceURL           string        `env:"KUBELOOP_DATASOURCE_URL"`
	DatasourceURLFile       string        `env:"KUBELOOP_DATASOURCE_URL_FILE"`
	ControlPlaneReplicas    int           `env:"KUBELOOP_CONTROL_PLANE_REPLICAS,default=1"`
	ConnectTimeout          time.Duration `env:"KUBELOOP_DATASOURCE_CONNECT_TIMEOUT"`
	QueryTimeout            time.Duration `env:"KUBELOOP_DATASOURCE_QUERY_TIMEOUT"`
	MaxOpenConnections      int           `env:"KUBELOOP_DATASOURCE_MAX_OPEN_CONNECTIONS"`
	MaxIdleConnections      int           `env:"KUBELOOP_DATASOURCE_MAX_IDLE_CONNECTIONS"`
	ConnectionMaxLifetime   time.Duration `env:"KUBELOOP_DATASOURCE_CONNECTION_MAX_LIFETIME"`
	TransactionMaxRetries   int           `env:"KUBELOOP_DATASOURCE_TRANSACTION_MAX_RETRIES"`
	TransactionRetryBackoff time.Duration `env:"KUBELOOP_DATASOURCE_TRANSACTION_RETRY_BACKOFF"`
	AllowInsecureDatasource bool          `env:"KUBELOOP_DATASOURCE_ALLOW_INSECURE"`
}

func ConfigFromEnv() (Config, error) {
	var environment storageEnvironment
	if _, err := env.UnmarshalFromEnviron(&environment); err != nil {
		return Config{}, fmt.Errorf("decode storage environment: %w", err)
	}
	config := Config{
		SQLitePath: strings.TrimSpace(environment.SQLitePath), DatasourceURL: strings.TrimSpace(environment.DatasourceURL),
		ControlPlaneReplicas: environment.ControlPlaneReplicas, ConnectTimeout: environment.ConnectTimeout,
		QueryTimeout: environment.QueryTimeout, MaxOpenConnections: environment.MaxOpenConnections,
		MaxIdleConnections: environment.MaxIdleConnections, ConnectionMaxLifetime: environment.ConnectionMaxLifetime,
		TransactionMaxRetries: environment.TransactionMaxRetries, TransactionRetryBackoff: environment.TransactionRetryBackoff,
		AllowInsecureDatasource: environment.AllowInsecureDatasource,
	}
	if file := strings.TrimSpace(environment.DatasourceURLFile); file != "" {
		if config.DatasourceURL != "" {
			return Config{}, errors.New("configure only one of KUBELOOP_DATASOURCE_URL and KUBELOOP_DATASOURCE_URL_FILE")
		}
		content, err := os.ReadFile(filepath.Clean(file))
		if err != nil {
			return Config{}, errors.New("read datasource URL file")
		}
		config.DatasourceURL = strings.TrimSpace(string(content))
	}
	return Normalize(config)
}

// Normalize validates and applies defaults to Control Plane storage settings.
func Normalize(config Config) (Config, error) {
	config.DatasourceURL = strings.TrimSpace(config.DatasourceURL)
	backend, err := datasourceBackend(config.DatasourceURL)
	if err != nil {
		return Config{}, err
	}
	if config.Backend != "" && config.Backend != backend {
		return Config{}, errors.New("storage backend conflicts with datasource URL")
	}
	config.Backend = backend
	if config.ControlPlaneReplicas < 0 {
		return Config{}, errors.New("Control Plane replicas must not be negative")
	}
	if config.ControlPlaneReplicas == 0 {
		config.ControlPlaneReplicas = 1
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
	if config.Backend == BackendSQLite {
		if config.ControlPlaneReplicas != 1 {
			return Config{}, errors.New("SQLite storage requires exactly one Control Plane replica")
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
		return Config{}, errors.New("datasource max open connections must not be negative")
	}
	if config.MaxOpenConnections == 0 {
		config.MaxOpenConnections = DefaultMaxOpen
	}
	if config.MaxIdleConnections < 0 {
		return Config{}, errors.New("datasource max idle connections must not be negative")
	}
	if config.MaxIdleConnections == 0 {
		config.MaxIdleConnections = min(DefaultMaxIdle, config.MaxOpenConnections)
	}
	if config.MaxIdleConnections > config.MaxOpenConnections {
		return Config{}, errors.New("datasource max idle connections must not exceed max open connections")
	}
	if config.TransactionMaxRetries < 0 || config.TransactionMaxRetries > 10 {
		return Config{}, errors.New("datasource transaction max retries must be between 0 and 10")
	}
	if config.TransactionMaxRetries == 0 {
		config.TransactionMaxRetries = DefaultTransactionMaxRetries
	}
	if config.TransactionRetryBackoff < 0 || config.TransactionRetryBackoff > time.Second {
		return Config{}, errors.New("datasource transaction retry backoff must be between 0 and 1s")
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
	case "mysql":
		return BackendMySQL, nil
	default:
		return "", errors.New("datasource URL must use postgresql or mysql")
	}
}

func validateDatasource(config Config) error {
	switch config.Backend {
	case BackendPostgreSQL:
		parsed, err := pgx.ParseConfig(config.DatasourceURL)
		if err != nil {
			return errors.New("PostgreSQL datasource URL is invalid")
		}
		if !postgreSQLTLSRequired(parsed) && !config.AllowInsecureDatasource {
			return errors.New("PostgreSQL datasource TLS is required; set sslmode=require or stronger")
		}
	case BackendMySQL:
		parsed, err := parseMySQLURL(config.DatasourceURL)
		if err != nil {
			return err
		}
		if !mysqlTLSRequired(parsed.TLSConfig) && !config.AllowInsecureDatasource {
			return errors.New("MySQL datasource TLS is required; set tls=true")
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
	return value != "" && value != "false" && value != "preferred" && value != "skip-verify"
}

func parseMySQLURL(raw string) (*mysql.Config, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "mysql" || parsed.Host == "" || strings.TrimPrefix(parsed.Path, "/") == "" {
		return nil, errors.New("MySQL datasource URL is invalid")
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
			return nil, errors.New("MySQL datasource URL contains duplicate parameters")
		}
		switch key {
		case "tls":
			config.TLSConfig = values[0]
		case "charset", "collation", "time_zone":
			config.Params[key] = values[0]
		default:
			return nil, fmt.Errorf("MySQL datasource URL parameter %q is not supported", key)
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
