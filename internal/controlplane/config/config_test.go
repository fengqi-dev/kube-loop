package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestLoadConfig(t *testing.T) {
	directory := t.TempDir()
	dsnPath := filepath.Join(directory, "dsn")
	if err := os.WriteFile(
		dsnPath,
		[]byte("postgres://user:secret@database/test?sslmode=require\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "kubeloop.yaml")
	raw := `
controlPlane:
  api:
    publicURL: https://kubeloop.example.com
  authentication:
    oauth:
      oidcSigningKeyFile: /run/secrets/oidc.pem
      hmacSecretFile: /run/secrets/hmac
      accessTTL: 7m
      refreshTTL: 48h
  admin:
    bootstrap:
      enabled: true
      username: root-admin
      displayName: Root Administrator
  kubernetes:
    timeout: 20s
  relay:
    ticket:
      signingKeyFile: /run/secrets/relay.pem
      ttl: 45s
    registry:
      listen: :9443
      certificateFile: /run/secrets/tls.crt
      privateKeyFile: /run/secrets/tls.key
      authentication: tokenreview
      namespace: kubeloop
      serviceAccount: gateway
  sessions:
    ttl: 90s
    maxLifetime: 4h
  storage:
    datasourceURLFile: ` + dsnPath + `
    replicas: 2
    connectTimeout: 8s
    queryTimeout: 4s
    maxOpenConnections: 12
    maxIdleConnections: 4
    connectionMaxLifetime: 20m
    transactionMaxRetries: 4
    transactionRetryBackoff: 40ms
  maintenance:
    interval: 30s
    batchSize: 50
  files:
    maxBytes: 1048576
    allowedRoots: [/workspace]
  logging:
    level: warn
gateway: {}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Document.API.Listen != ":8080" || config.AccessTokenTTL != 7*time.Minute {
		t.Fatalf("api=%#v accessTTL=%s", config.Document.API, config.AccessTokenTTL)
	}
	if config.Storage.Backend != controlplanestorage.BackendPostgreSQL || config.Storage.ControlPlaneReplicas != 2 {
		t.Fatalf("storage=%#v", config.Storage)
	}
	if config.Storage.DatasourceURL != "postgres://user:secret@database/test?sslmode=require" {
		t.Fatal("datasource URL file was not loaded")
	}
	if !config.Document.Admin.Bootstrap.Enabled || config.Document.Admin.Bootstrap.Username != "root-admin" {
		t.Fatalf("admin bootstrap=%+v", config.Document.Admin.Bootstrap)
	}
}

func TestLoadConfigDefaultsAccessTokenToOneDay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	raw := `
controlPlane:
  api:
    publicURL: https://kubeloop.example.com
  authentication:
    oauth:
      oidcSigningKeyFile: /run/secrets/oidc.pem
      hmacSecretFile: /run/secrets/hmac
  relay:
    ticket:
      signingKeyFile: /run/secrets/relay.pem
    registry:
      certificateFile: /run/secrets/tls.crt
      privateKeyFile: /run/secrets/tls.key
      namespace: kubeloop
      serviceAccount: gateway
gateway: {}
`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.AccessTokenTTL != 24*time.Hour {
		t.Fatalf("AccessTokenTTL = %s, want 24h", config.AccessTokenTTL)
	}
	if config.RefreshTokenTTL != 30*24*time.Hour {
		t.Fatalf("RefreshTokenTTL = %s, want 720h", config.RefreshTokenTTL)
	}
}

func TestLoadConfigRejectsUnknownFields(t *testing.T) {
	tests := map[string]struct {
		raw   string
		field string
	}{
		"api field": {
			raw:   "controlPlane:\n  api:\n    publicURL: https://example.com\n    typo: true\ngateway: {}\n",
			field: "typo",
		},
		"separate admin listener": {
			raw:   "controlPlane:\n  admin:\n    listen: :8081\ngateway: {}\n",
			field: "listen",
		},
		"separate admin public URL": {
			raw:   "controlPlane:\n  admin:\n    publicURL: https://admin.example.com\ngateway: {}\n",
			field: "publicURL",
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kubeloop.yaml")
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(err.Error(), test.field) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestLoadConfigRequiresPositiveDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	if err := os.WriteFile(path, []byte("controlPlane:\n  sessions:\n    ttl: 0s\ngateway: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadConfig(path)
	if err == nil || !strings.Contains(err.Error(), "sessions.ttl") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadConfigRequiresUnifiedDocument(t *testing.T) {
	tests := map[string]string{
		"legacy root":     "api:\n  publicURL: https://example.com\n",
		"missing gateway": "controlPlane:\n  api:\n    publicURL: https://example.com\n",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "kubeloop.yaml")
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("non-unified configuration was accepted")
			}
		})
	}
}
