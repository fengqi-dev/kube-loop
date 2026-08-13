package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaultsToDisabledDenyAllManagementAccess(t *testing.T) {
	config, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Bootstrap.Subjects) != 0 || len(config.Bootstrap.Groups) != 0 || config.Bootstrap.RecoveryEnabled {
		t.Fatalf("bootstrap = %#v", config.Bootstrap)
	}
	if config.BreakGlass.Enabled || config.BreakGlass.ParsedSessionTTL() != 15*time.Minute || len(config.BreakGlass.ParsedSourceCIDRs()) != 0 {
		t.Fatalf("break-glass = %#v", config.BreakGlass)
	}
}

func TestLoadStrictManagementConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "management.json")
	document := `{
		"bootstrap":{"subjects":["00000000-0000-4000-8000-000000000001"],"groups":["platform-bootstrap"],"recoveryEnabled":false},
		"breakGlass":{
			"enabled":true,
			"secretAlias":"emergency",
			"secretFile":"/var/run/secrets/kubeloop/management/break-glass/emergency/credential",
			"sessionTtl":"10m",
			"allowedSourceCidrs":["10.0.0.0/8","2001:db8::/32"]
		}
	}`
	if err := os.WriteFile(path, []byte(document), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Bootstrap.Subjects[0] != "00000000-0000-4000-8000-000000000001" || config.Bootstrap.Groups[0] != "platform-bootstrap" {
		t.Fatalf("bootstrap = %#v", config.Bootstrap)
	}
	if config.BreakGlass.ParsedSessionTTL() != 10*time.Minute || len(config.BreakGlass.ParsedSourceCIDRs()) != 2 {
		t.Fatalf("break-glass = %#v", config.BreakGlass)
	}
}

func TestLoadRejectsSecretMaterialAndUnsafeBreakGlassConfiguration(t *testing.T) {
	tests := []struct {
		name     string
		document string
	}{
		{name: "unknown secret material", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":false,"secret":"plaintext"}}`},
		{name: "wildcard bootstrap", document: `{"bootstrap":{"subjects":["*"],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":false}}`},
		{name: "enabled without alias", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":true,"secretFile":"/var/run/secrets/kubeloop/management/break-glass/emergency/credential"}}`},
		{name: "arbitrary secret path", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":true,"secretAlias":"emergency","secretFile":"/tmp/credential"}}`},
		{name: "long session", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":true,"secretAlias":"emergency","secretFile":"/var/run/secrets/kubeloop/management/break-glass/emergency/credential","sessionTtl":"16m"}}`},
		{name: "uncanonical CIDR", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":true,"secretAlias":"emergency","secretFile":"/var/run/secrets/kubeloop/management/break-glass/emergency/credential","allowedSourceCidrs":["10.1.2.3/8"]}}`},
		{name: "disabled with alias", document: `{"bootstrap":{"subjects":[],"groups":[],"recoveryEnabled":false},"breakGlass":{"enabled":false,"secretAlias":"emergency"}}`},
		{name: "removed provider aliases", document: `{"providerSecretAliases":{}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "management.json")
			if err := os.WriteFile(path, []byte(test.document), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("unsafe management configuration succeeded")
			}
		})
	}
}

func TestLoadRejectsOversizedAndMultipleDocuments(t *testing.T) {
	directory := t.TempDir()
	oversized := filepath.Join(directory, "oversized.json")
	if err := os.WriteFile(oversized, []byte(strings.Repeat("x", MaximumConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(oversized); err == nil {
		t.Fatal("oversized management configuration succeeded")
	}
	multiple := filepath.Join(directory, "multiple.json")
	if err := os.WriteFile(multiple, []byte(`{} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(multiple); err == nil {
		t.Fatal("multiple management configuration documents succeeded")
	}
}
