package storage

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

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
