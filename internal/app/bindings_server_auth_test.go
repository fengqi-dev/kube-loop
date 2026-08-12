package app

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestTokenUserNamePrefersOIDCPreferredUserName(t *testing.T) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"subject-1","email":"user@example.test","name":"Example User","preferred_username":"fengqi"}`))
	if got := tokenUserName("header." + payload + ".signature"); got != "fengqi" {
		t.Fatalf("token username = %q", got)
	}
	if got := tokenUserName("opaque-token"); got != "" {
		t.Fatalf("opaque token username = %q", got)
	}
}

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

func TestAppAnonymousLoginUsesAdvertisedMethodAndSecureCredentialStore(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case clientdiscovery.Path:
			_ = json.NewEncoder(writer).Encode(clientdiscovery.Document{
				ServiceID: "service-1", PublicURL: server.URL, APIVersions: []string{"v2"},
				AuthMethods: []clientdiscovery.AuthMethod{
					{ID: "guest", Type: "anonymous", Interaction: "none"},
				},
				ProtocolMin: "2.0", ProtocolMax: "2.0",
			})
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(writer).Encode(map[string]any{"issuer": server.URL,
				"authorization_endpoint": server.URL + "/oauth2/authorize", "token_endpoint": server.URL + "/oauth2/token",
				"revocation_endpoint": server.URL + "/oauth2/revoke"})
		case "/oauth2/token":
			writeAppTokenResponse(writer, "anonymous-access", "anonymous-refresh")
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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
	if _, err := application.LoginServerAnonymous("service-1", "guest"); err != nil {
		t.Fatal(err)
	}
	if credentialStore.values["service-1"].AccessToken != "anonymous-access" {
		t.Fatalf("anonymous credential = %#v", credentialStore.values["service-1"])
	}
}

func TestLogoutClearsLocalCredentialsWhenRemoteRevokeFails(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
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
		"token_type": "Bearer", "access_token": access, "refresh_token": refresh,
		"expires_in": 60, "refresh_expires_in": 3600,
	})
}
