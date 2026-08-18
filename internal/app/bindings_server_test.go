package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/singbox/distribution"
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
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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

func TestBootstrapWithActiveProfileDoesNotReadKubeconfig(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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
	if data.ServerProfiles.ActiveProfileID != "service-1" {
		t.Fatalf("bootstrap = %#v", data)
	}
	if data.CoreVersion != distribution.Version {
		t.Fatalf("core version = %q, want %q", data.CoreVersion, distribution.Version)
	}
}

func TestBootstrapWithoutProfileDoesNotReadKubeconfig(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "invalid.yaml"))
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}
	data, err := application.Bootstrap()
	if err != nil {
		t.Fatal(err)
	}
	if len(data.ServerProfiles.Profiles) != 0 {
		t.Fatalf("bootstrap = %#v", data)
	}
}

func TestSaveServerProfileRejectsServiceIDCollisionAcrossOrigins(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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

func TestSaveServerProfileEditsAddressForSameService(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{
		ID: "service-1", BaseURL: "https://old.example.test", DisplayName: "Old name", LastUserName: "fengqi",
	}); err != nil {
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
	result, err := application.SaveServerProfile(SaveServerProfileRequest{
		ID: "service-1", BaseURL: server.URL, DisplayName: "New name",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.BaseURL != server.URL || result.Profile.DisplayName != "New name" || result.Profile.LastUserName != "fengqi" {
		t.Fatalf("edited profile = %#v", result.Profile)
	}
}

func TestSaveServerProfilePreservesRequestedHTTPScheme(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
			ServiceID: "service-1", PublicURL: "https://" + strings.TrimPrefix(server.URL, "http://"),
			APIVersions: []string{"v2"}, ProtocolMin: "2.0", ProtocolMax: "2.0",
		})
	}))
	defer server.Close()
	application := &App{profiles: profileStore, discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()})}
	result, err := application.SaveServerProfile(SaveServerProfileRequest{BaseURL: server.URL, Activate: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Profile.BaseURL != server.URL {
		t.Fatalf("profile base URL = %q, want requested HTTP origin %q", result.Profile.BaseURL, server.URL)
	}
}

func TestSaveServerProfileRejectsEditToDifferentService(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: "https://old.example.test"}); err != nil {
		t.Fatal(err)
	}
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
			ServiceID: "service-2", PublicURL: server.URL, APIVersions: []string{"v2"}, ProtocolMin: "2.0", ProtocolMax: "2.0",
		})
	}))
	defer server.Close()
	application := &App{profiles: profileStore, discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()})}
	if _, err := application.SaveServerProfile(SaveServerProfileRequest{ID: "service-1", BaseURL: server.URL}); err == nil {
		t.Fatal("editing a profile to a different service was accepted")
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
