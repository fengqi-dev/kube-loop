package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/clientv2/auth"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/clientv2/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
)

type memoryCredentialStore struct {
	values map[string]credentials.Credential
}

func (store *memoryCredentialStore) Set(profileID string, credential credentials.Credential) error {
	store.values[profileID] = credential
	return nil
}

func (store *memoryCredentialStore) Get(profileID string) (credentials.Credential, error) {
	credential, ok := store.values[profileID]
	if !ok {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return credential, nil
}

func (store *memoryCredentialStore) Delete(profileID string) error {
	delete(store.values, profileID)
	return nil
}

func TestAppADLoginStoresTokensOutsideProfileAndLogoutRevokes(t *testing.T) {
	var server *httptest.Server
	revoked := false
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case clientdiscovery.Path:
			_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
				ServiceID: "service-1", PublicURL: server.URL, APIVersions: []string{"v2"},
				AuthMethods: []clientdiscovery.AuthMethod{{ID: "corp", Type: "ad", DisplayName: "Corporate AD", Interaction: "password"}},
				ProtocolMin: "2.0", ProtocolMax: "2.0",
			})
		case "/auth/ad/corp/login":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["username"] != "ada" || body["password"] != "secret" || body["deviceId"] == "" {
				t.Fatalf("login body = %#v", body)
			}
			writeAppTokenResponse(writer, "access-1", "refresh-1")
		case "/auth/token/revoke":
			revoked = true
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{}}
	application := &App{
		profiles: profileStore, credentials: credentialStore,
		discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()}),
		auth:      clientauth.New(clientauth.Config{HTTPClient: server.Client()}),
	}
	session, err := application.LoginServerAD("service-1", "corp", "ada", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if !session.Authenticated || session.AccessExpiresAt.IsZero() {
		t.Fatalf("session = %#v", session)
	}
	if profileStore.Snapshot().Profiles[0].LastUserName != "ada" {
		t.Fatalf("profile = %#v", profileStore.Snapshot().Profiles[0])
	}
	credential := credentialStore.values["service-1"]
	if credential.AccessToken != "access-1" || credential.RefreshToken != "refresh-1" {
		t.Fatalf("stored credential = %#v", credential)
	}
	if err := application.LogoutServer("service-1"); err != nil {
		t.Fatal(err)
	}
	if !revoked || len(credentialStore.values) != 0 {
		t.Fatalf("revoked = %t, credentials = %#v", revoked, credentialStore.values)
	}
}

func TestAppRejectsProviderNotAdvertisedByProfile(t *testing.T) {
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
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	application := &App{
		profiles: profileStore, credentials: &memoryCredentialStore{values: map[string]credentials.Credential{}},
		discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()}),
		auth:      clientauth.New(clientauth.Config{HTTPClient: server.Client()}),
	}
	if _, err := application.LoginServerAD("service-1", "corp", "ada", "secret"); err == nil {
		t.Fatal("unadvertised AD provider was accepted")
	}
}

func TestAppDevelopmentLoginUsesAdvertisedMethodAndSecureCredentialStore(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case clientdiscovery.Path:
			_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
				ServiceID: "service-1", PublicURL: server.URL, APIVersions: []string{"v2"},
				AuthMethods: []clientdiscovery.AuthMethod{
					{ID: "local", Type: "static-token", Interaction: "token"},
					{ID: "guest", Type: "anonymous", Interaction: "none"},
				},
				ProtocolMin: "2.0", ProtocolMax: "2.0",
			})
		case "/auth/static-token/local/login":
			var body map[string]string
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["token"] != "development-secret" || body["deviceId"] == "" {
				t.Fatalf("static-token body = %#v", body)
			}
			writeAppTokenResponse(writer, "static-access", "static-refresh")
		case "/auth/anonymous/guest/login":
			writeAppTokenResponse(writer, "anonymous-access", "anonymous-refresh")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: server.URL}); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{}}
	application := &App{
		profiles: profileStore, credentials: credentialStore,
		discovery: clientdiscovery.New(clientdiscovery.Config{HTTPClient: server.Client()}),
		auth:      clientauth.New(clientauth.Config{HTTPClient: server.Client()}),
	}
	if _, err := application.LoginServerStaticToken("service-1", "local", "development-secret"); err != nil {
		t.Fatal(err)
	}
	if credentialStore.values["service-1"].AccessToken != "static-access" {
		t.Fatalf("static credential = %#v", credentialStore.values["service-1"])
	}
	if _, err := application.LoginServerAnonymous("service-1", "guest"); err != nil {
		t.Fatal(err)
	}
	if credentialStore.values["service-1"].AccessToken != "anonymous-access" {
		t.Fatalf("anonymous credential = %#v", credentialStore.values["service-1"])
	}
}

func TestLogoutClearsLocalCredentialsWhenRemoteRevokeFails(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: "https://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{"service-1": {
		AccessToken: "access", RefreshToken: "refresh", DeviceID: "device",
	}}}
	application := &App{profiles: profileStore, credentials: credentialStore, auth: clientauth.New(clientauth.Config{RequestTimeout: 20 * time.Millisecond})}
	err = application.LogoutServer("service-1")
	if err == nil {
		t.Fatal("failed remote revoke was not reported")
	}
	if len(credentialStore.values) != 0 {
		t.Fatalf("credentials remained after failed revoke: %#v", credentialStore.values)
	}
}

func TestDeleteServerProfileClearsLocalStateWhenRemoteRevokeFails(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{ID: "service-1", BaseURL: "https://127.0.0.1:1"}); err != nil {
		t.Fatal(err)
	}
	credentialStore := &memoryCredentialStore{values: map[string]credentials.Credential{"service-1": {
		AccessToken: "access", RefreshToken: "refresh", DeviceID: "device",
	}}}
	application := &App{profiles: profileStore, credentials: credentialStore, auth: clientauth.New(clientauth.Config{RequestTimeout: 20 * time.Millisecond})}
	state, err := application.DeleteServerProfile("service-1")
	if err == nil {
		t.Fatal("failed remote revoke was not reported")
	}
	if len(credentialStore.values) != 0 {
		t.Fatalf("credentials remained after failed revoke: %#v", credentialStore.values)
	}
	if len(state.Profiles) != 0 || len(profileStore.Snapshot().Profiles) != 0 {
		t.Fatalf("profile remained after failed revoke: %#v", state)
	}
}

func writeAppTokenResponse(writer http.ResponseWriter, access, refresh string) {
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"tokenType": "Bearer", "accessToken": access, "refreshToken": refresh,
		"accessExpiresAt": time.Now().Add(time.Minute), "refreshExpiresAt": time.Now().Add(time.Hour),
	})
}
