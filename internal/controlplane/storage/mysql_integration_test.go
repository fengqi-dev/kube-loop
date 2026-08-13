package storage

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// MySQL integration verification is opt in. The configured account must be
// allowed to create and drop an isolated test database.
func TestMySQLBackendIntegration(t *testing.T) {
	config, cleanup := newMySQLIntegrationConfig(t)
	defer cleanup()
	store, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if store.Backend() != BackendMySQL {
		t.Fatalf("backend = %q", store.Backend())
	}
	testRepositoryConformance(t, store)
}

func TestMySQLRestartRecovery(t *testing.T) {
	config, cleanup := newMySQLIntegrationConfig(t)
	defer cleanup()
	testRestartRecovery(t, config)
}

func newMySQLIntegrationConfig(t *testing.T) (Config, func()) {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv("KUBELOOP_TEST_MYSQL_DATASOURCE_URL"))
	if raw == "" {
		t.Skip("KUBELOOP_TEST_MYSQL_DATASOURCE_URL is not configured")
	}
	parsedURL, err := url.Parse(raw)
	if err != nil || parsedURL.Scheme != "mysql" {
		t.Fatal("KUBELOOP_TEST_MYSQL_DATASOURCE_URL must be a mysql:// URL")
	}
	adminConfig, err := parseMySQLURL(raw)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	databaseName := "kubeloop_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE `"+databaseName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		_ = admin.Close()
		t.Fatal("create MySQL integration database")
	}
	testURL := *parsedURL
	testURL.Path = "/" + databaseName
	cleanup := func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = admin.ExecContext(cleanupContext, "DROP DATABASE `"+databaseName+"`")
		_ = admin.Close()
	}
	return Config{
		DatasourceURL: testURL.String(), ConnectTimeout: 10 * time.Second, QueryTimeout: 5 * time.Second,
		MaxOpenConnections: 10, MaxIdleConnections: 5,
		AllowInsecureDatasource: !mysqlTLSRequired(adminConfig.TLSConfig),
	}, cleanup
}
