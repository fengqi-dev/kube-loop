package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/clientv2/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
)

func TestSaveSelectAndDeleteServerProfile(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
			ServiceID: "service-1", PublicURL: server.URL, APIVersions: []string{"v2"},
			AuthMethods: []clientdiscovery.AuthMethod{{ID: "company", Type: "oidc", Interaction: "browser"}},
			ProtocolMin: "2.0", ProtocolMax: "2.0",
		})
	}))
	defer server.Close()
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &App{
		profiles:  profileStore,
		discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()}),
	}
	result, err := application.SaveServerProfile(SaveServerProfileRequest{BaseURL: server.URL, DisplayName: "Production", Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.ID != "service-1" || application.ServerProfiles().ActiveProfileID != "service-1" {
		t.Fatalf("result = %#v, state = %#v", result, application.ServerProfiles())
	}
	state, err := application.DeleteServerProfile("service-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Profiles) != 0 || state.ActiveProfileID != "" {
		t.Fatalf("state after delete = %#v", state)
	}
}

func TestBootstrapWithActiveV2ProfileDoesNotReadKubeconfig(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: "https://gateway.example.test"}); err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}
	data, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if data.Mode != "v2" || data.BackendMode != BackendModeRemote || data.ServerProfiles.ActiveProfileID != "service-1" {
		t.Fatalf("bootstrap = %#v", data)
	}
}

func TestBootstrapWithoutProfileUsesV2SetupWithoutReadingKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "invalid.yaml"))
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}
	data, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if data.Mode != "setup" || data.BackendMode != BackendModeRemote || len(data.ServerProfiles.Profiles) != 0 {
		t.Fatalf("bootstrap = %#v", data)
	}
}

func TestBootstrapReportsByteOnlyV1StateBackup(t *testing.T) {
	root := t.TempDir()
	legacy := []byte(`{"kubeconfigFiles":["/path/that/must/not/be/opened"],"clusters":{"dev":{"connected":true}}}`)
	if err := os.WriteFile(filepath.Join(root, "state.json"), legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	application := newApp("test", nil, appDependencies{
		profilePath:     filepath.Join(root, "servers-v2.json"),
		credentialStore: &memoryCredentialStore{values: map[string]credentials.Credential{}},
	})
	data, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if !data.Migration.LegacyDetected || data.Migration.Error != "" || data.Migration.BackupPath == "" {
		t.Fatalf("migration = %#v", data.Migration)
	}
	backup, err := os.ReadFile(data.Migration.BackupPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(backup) != string(legacy) {
		t.Fatalf("legacy backup changed: %q", backup)
	}
}

func TestSaveServerProfileRejectsServiceIDCollisionAcrossOrigins(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: "https://existing.example.test"}); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
			ServiceID: "service-1", PublicURL: server.URL, APIVersions: []string{"v2"}, ProtocolMin: "2.0", ProtocolMax: "2.0",
		})
	}))
	defer server.Close()
	application := &App{profiles: profileStore, discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()})}
	if _, err := application.SaveServerProfile(SaveServerProfileRequest{BaseURL: server.URL}); err == nil {
		t.Fatal("service ID collision was accepted")
	}
}

func TestSafeDownloadNameRemovesPathAndPlatformReservedCharacters(t *testing.T) {
	for input, expected := range map[string]string{
		"": "download", "../report.txt": "report.txt", `bad:name?.txt`: "bad_name_.txt", ".": "download",
	} {
		if actual := safeDownloadName(input); actual != expected {
			t.Fatalf("safeDownloadName(%q) = %q, want %q", input, actual, expected)
		}
	}
}
