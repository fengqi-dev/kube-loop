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
	path := filepath.Join(directory, "control-plane.yaml")
	raw := `
api:
  publicURL: https://kubeloop.example.com
authentication:
  providers: []
  token:
    signingKeyFile: /run/secrets/token.pem
    accessTTL: 7m
    refreshTTL: 48h
authorization:
  version: 1
  rules:
    - id: developers
      groups: [developers]
      namespaces: ["*"]
      operations: ["*"]
      resourceKinds: ["*"]
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
	if len(config.Authentication.Providers) != 0 || len(config.Policy.Rules) != 1 {
		t.Fatalf("authentication=%#v policy=%#v", config.Authentication, config.Policy)
	}
}

func TestLoadControlPlaneConfigRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.yaml")
	if err := os.WriteFile(path, []byte("api:\n  publicURL: https://example.com\n  typo: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadControlPlaneConfig(path)
	if err == nil || !strings.Contains(err.Error(), "typo") {
		t.Fatalf("error=%v", err)
	}
}

func TestLoadControlPlaneConfigRequiresPositiveDurations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control-plane.yaml")
	if err := os.WriteFile(path, []byte("sessions:\n  ttl: 0s\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadControlPlaneConfig(path)
	if err == nil || !strings.Contains(err.Error(), "sessions.ttl") {
		t.Fatalf("error=%v", err)
	}
}
