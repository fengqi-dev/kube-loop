package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	controlplanestorage "github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
)

func TestLoadControlPlaneConfig(t *testing.T) {
	directory := t.TempDir()
	dsnPath := filepath.Join(directory, "dsn")
	if err := os.WriteFile(dsnPath, []byte("postgres://user:secret@database/test?sslmode=require\n"), 0o600); err != nil {
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
  management:
    publicURL: https://kubeloop.example.com
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
	config, err := loadControlPlaneConfig(path)
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
}

func TestLoadControlPlaneConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	if err := os.WriteFile(path, []byte("controlPlane:\n  api:\n    publicURL: https://example.com\n    typo: true\ngateway: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadControlPlaneConfig(path)
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadControlPlaneConfigRequiresPositiveDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubeloop.yaml")
	if err := os.WriteFile(path, []byte("controlPlane:\n  sessions:\n    ttl: 0s\ngateway: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadControlPlaneConfig(path)
	if err == nil || !strings.Contains(err.Error(), "sessions.ttl") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadControlPlaneConfigRequiresUnifiedDocument(t *testing.T) {
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
			if _, err := loadControlPlaneConfig(path); err == nil {
				t.Fatal("non-unified configuration was accepted")
			}
		})
	}
}
