package storage

import (
	"errors"
	"path/filepath"
	"strings"
	"time"
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

func (config Config) normalized() (Config, error) { return Normalize(config) }
