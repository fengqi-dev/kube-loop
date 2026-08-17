package storage

import (
	"os"
	"strings"
	"testing"
)

func externalDatabaseTestURL(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value != "" {
		return value
	}
	if os.Getenv("KUBELOOP_REQUIRE_DATABASE_INTEGRATION") == "1" {
		t.Fatalf("%s is required by the database integration gate", name)
	}
	t.Skipf("%s is not configured", name)
	return ""
}
