package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestIAMResourcesUseLowerCamelCaseJSONWithoutSecretHashes(t *testing.T) {
	now := time.Date(2026, time.August, 14, 10, 0, 0, 0, time.UTC)
	resources := []any{
		Organization{ID: "organization-id", Name: "KubeLoop", Slug: "kubeloop", Status: "active", CreatedAt: now, UpdatedAt: now},
		Group{ID: "group-id", OrganizationID: "organization-id", Name: "Administrators", System: true, CreatedAt: now, UpdatedAt: now},
		GroupNamespace{GroupID: "group-id", Namespace: "payments", CreatedAt: now},
		Invitation{ID: "invitation-id", OrganizationID: "organization-id", Email: "admin@example.test", GroupID: "group-id", TokenHash: []byte("must-not-leak"), Status: "pending", CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
	}
	encoded, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	document := string(encoded)
	for _, want := range []string{`"id":"organization-id"`, `"organizationId":"organization-id"`, `"groupId":"group-id"`, `"namespace":"payments"`, `"system":true`} {
		if !strings.Contains(document, want) {
			t.Fatalf("IAM JSON is missing %s: %s", want, document)
		}
	}
	for _, forbidden := range []string{`"ID"`, `"OrganizationID"`, `"TokenHash"`, "must-not-leak"} {
		if strings.Contains(document, forbidden) {
			t.Fatalf("IAM JSON exposes forbidden value %q: %s", forbidden, document)
		}
	}
}
