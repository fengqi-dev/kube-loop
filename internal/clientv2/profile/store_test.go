package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerProfileStorePersistsOnlyNonSecretV2State(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers-v2.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{
		ID: "service-1", BaseURL: "https://gateway.example.test/",
		TunnelPath:  "/relay/tunnel",
		DisplayName: "Production", LastPrincipalID: "principal-1",
		LastUserName: "Ada", LastNamespace: "payments",
	}
	if err := store.Upsert(profile); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := reopened.Snapshot()
	if state.ActiveProfileID != profile.ID || len(state.Profiles) != 1 ||
		state.Profiles[0].SchemaVersion != ProfileSchemaVersion ||
		state.Profiles[0].BaseURL != "https://gateway.example.test" || state.Profiles[0].TunnelPath != "/relay/tunnel" {
		t.Fatalf("state = %#v", state)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"token", "password", "clientSecret", "kubeconfig"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(forbidden)) {
			t.Fatalf("profile store contains forbidden field %q: %s", forbidden, raw)
		}
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
}

func TestServerProfileObjectSchemaMigratesLegacyAndRejectsFutureVersion(t *testing.T) {
	legacy, err := decodeState([]byte(`{"version":1,"activeProfileId":"one","profiles":[{"id":"one","baseUrl":"https://one.example.test","tunnelPath":"/tunnel"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if legacy.Profiles[0].SchemaVersion != ProfileSchemaVersion {
		t.Fatalf("legacy Profile schema version = %d", legacy.Profiles[0].SchemaVersion)
	}
	if _, err := decodeState([]byte(`{"version":1,"profiles":[{"schemaVersion":2,"id":"one","baseUrl":"https://one.example.test","tunnelPath":"/tunnel"}]}`)); err == nil {
		t.Fatal("future Server Profile object schema was accepted")
	}
}

func TestServerProfileStoreDefaultsAndValidatesTunnelPath(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Profiles[0].TunnelPath; got != "/tunnel" {
		t.Fatalf("default tunnel path = %q", got)
	}
	for _, value := range []string{"https://evil.test/tunnel", "//evil.test/tunnel", "/relay/../tunnel", "/tunnel?token=secret"} {
		if err := store.Upsert(Profile{ID: "two", BaseURL: "https://two.example.test", TunnelPath: value}); err == nil {
			t.Fatalf("unsafe tunnel path accepted: %q", value)
		}
	}
}

func TestServerProfileStoreRecoversFromValidBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers-v2.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "two", BaseURL: "https://two.example.test"}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if !recovered.RecoveredFromBackup() || len(recovered.Snapshot().Profiles) != 1 || recovered.Snapshot().Profiles[0].ID != "one" {
		t.Fatalf("recovered state = %#v", recovered.Snapshot())
	}
}

func TestServerProfileStoreRejectsSecretFieldsAndUnsafeURLs(t *testing.T) {
	for _, value := range []string{
		"http://gateway.example.test", "https://user@gateway.example.test",
		"https://gateway.example.test?token=secret", "https://gateway.example.test/team", "not-a-url",
	} {
		if _, err := NormalizeBaseURL(value); err == nil {
			t.Fatalf("unsafe service address accepted: %q", value)
		}
	}
	path := filepath.Join(t.TempDir(), "servers-v2.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"profiles":[],"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("profile store accepted unknown secret field")
	}
}

func TestNormalizeBaseURLAcceptsOnlyOneOrigin(t *testing.T) {
	got, err := NormalizeBaseURL("https://gateway.example.test/")
	if err != nil || got != "https://gateway.example.test" {
		t.Fatalf("normalized URL = %q, error = %v", got, err)
	}
	if _, err := NormalizeBaseURL("https://gateway.example.test/team%20one/"); err == nil {
		t.Fatal("service address with a path was accepted")
	}
}

func TestServerProfileStoreReturnsDefensiveSnapshots(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	snapshot.Profiles[0].BaseURL = "https://attacker.example.test"
	if store.Snapshot().Profiles[0].BaseURL != "https://one.example.test" {
		t.Fatal("snapshot mutated persisted profile")
	}
}

func TestServerProfileStoreSnapshotKeepsEmptyProfilesAsArray(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	if snapshot.Profiles == nil {
		t.Fatal("empty Profile snapshot must serialize as an array, not null")
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"profiles":[]`) {
		t.Fatalf("snapshot JSON = %s", raw)
	}
}
