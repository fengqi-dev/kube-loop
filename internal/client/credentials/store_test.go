package credentials

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

type memoryBackend struct {
	values map[string]string
	failAt int
	sets   int
}

func newMemoryBackend() *memoryBackend { return &memoryBackend{values: map[string]string{}} }

func (backend *memoryBackend) Set(service, account, secret string) error {
	backend.sets++
	if backend.failAt > 0 && backend.sets == backend.failAt {
		return errors.New("injected keyring failure")
	}
	backend.values[service+"/"+account] = secret
	return nil
}

func (backend *memoryBackend) Get(service, account string) (string, error) {
	value, ok := backend.values[service+"/"+account]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (backend *memoryBackend) Delete(service, account string) error {
	key := service + "/" + account
	if _, ok := backend.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(backend.values, key)
	return nil
}

func TestSystemStoreUsesVersionedKeyringEntries(t *testing.T) {
	backend := newMemoryBackend()
	store := NewStore(backend)
	credential := Credential{
		TokenType: "Bearer", AccessToken: "access-one", RefreshToken: "refresh-one", DeviceID: "device-1",
		AccessExpiresAt: time.Now().Add(time.Minute), RefreshExpiresAt: time.Now().Add(time.Hour),
		IdentityID: "identity-1", UserName: "Example User",
	}
	if err := store.Set("profile-1", credential); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	tokenMismatch := got.AccessToken != credential.AccessToken || got.RefreshToken != credential.RefreshToken
	identityMismatch := got.IdentityID != credential.IdentityID || got.UserName != credential.UserName
	if tokenMismatch || identityMismatch || got.DeviceID != credential.DeviceID {
		t.Fatalf("credential = %#v", got)
	}
	prefix, err := accountPrefix("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	generation := backend.values[serviceName+"/"+prefix+":current"]
	var details metadata
	if err := json.Unmarshal(
		[]byte(backend.values[serviceName+"/"+prefix+":"+generation+":metadata"]),
		&details,
	); err != nil {
		t.Fatal(err)
	}
	if details.SchemaVersion != credentialMetadataSchemaVersion {
		t.Fatalf("credential metadata schema version = %d", details.SchemaVersion)
	}
	credential.AccessToken = "access-two"
	credential.RefreshToken = "refresh-two"
	if err := store.Set("profile-1", credential); err != nil {
		t.Fatal(err)
	}
	if len(backend.values) != 4 {
		t.Fatalf("keyring entries = %d, want current pointer plus three active entries", len(backend.values))
	}
	if err := store.Delete("profile-1"); err != nil {
		t.Fatal(err)
	}
	if len(backend.values) != 0 {
		t.Fatalf("keyring entries remain: %#v", backend.values)
	}
	if _, err := store.Get("profile-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete error = %v", err)
	}
}

func TestKeyringServiceSeparatesReleaseAndDevelopment(t *testing.T) {
	t.Parallel()
	if got := keyringServiceForVersion("v2.1.0"); got != serviceName {
		t.Fatalf("release service = %q, want %q", got, serviceName)
	}
	for _, version := range []string{"", "dev"} {
		if got := keyringServiceForVersion(version); got != developmentServiceName {
			t.Fatalf("development service for %q = %q, want %q", version, got, developmentServiceName)
		}
	}
}

func TestSystemStoreSeparatesOAuthClients(t *testing.T) {
	backend := newMemoryBackend()
	desktop := newStoreForClient(backend, "v2.1.2", "kubeloop-desktop")
	tui := newStoreForClient(backend, "v2.1.2", "kubeloop-tui")
	desktopCredential := Credential{
		AccessToken: "desktop-access", RefreshToken: "desktop-refresh", DeviceID: "desktop-device",
	}
	tuiCredential := Credential{
		AccessToken: "tui-access", RefreshToken: "tui-refresh", DeviceID: "tui-device",
	}
	if err := desktop.Set("profile-1", desktopCredential); err != nil {
		t.Fatal(err)
	}
	if err := tui.Set("profile-1", tuiCredential); err != nil {
		t.Fatal(err)
	}
	gotDesktop, err := desktop.Get("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	gotTUI, err := tui.Get("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	if gotDesktop.RefreshToken != desktopCredential.RefreshToken || gotTUI.RefreshToken != tuiCredential.RefreshToken {
		t.Fatalf("client credentials collided: desktop=%#v tui=%#v", gotDesktop, gotTUI)
	}
	if desktop.service == tui.service || desktop.service == serviceName || tui.service == serviceName {
		t.Fatalf("client keyring services are not isolated: desktop=%q tui=%q", desktop.service, tui.service)
	}
}

func TestClientKeyringServiceSeparatesDevelopmentChannel(t *testing.T) {
	release := keyringServiceForClient("v2.1.2", "kubeloop-tui")
	development := keyringServiceForClient("dev", "kubeloop-tui")
	if release == development || !strings.Contains(release, "kubeloop-tui") ||
		!strings.Contains(development, "kubeloop-tui") {
		t.Fatalf("client services release=%q development=%q", release, development)
	}
}

func TestSystemStoreRequiresCurrentMetadataSchema(t *testing.T) {
	backend := newMemoryBackend()
	store := NewStore(backend)
	prefix, err := accountPrefix("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	install := func(metadataJSON string) {
		backend.values[serviceName+"/"+prefix+":current"] = "generation"
		backend.values[serviceName+"/"+prefix+":generation:access"] = "access"
		backend.values[serviceName+"/"+prefix+":generation:refresh"] = "refresh"
		backend.values[serviceName+"/"+prefix+":generation:metadata"] = metadataJSON
	}
	install(`{"deviceId":"device-1"}`)
	if _, err := store.Get("profile-1"); err == nil {
		t.Fatal("unversioned credential metadata was accepted")
	}
	install(`{"schemaVersion":1,"deviceId":"device-1"}`)
	if credential, err := store.Get("profile-1"); err != nil || credential.DeviceID != "device-1" {
		t.Fatalf("version 1 metadata credential = %#v err = %v", credential, err)
	}
	install(`{"schemaVersion":2,"deviceId":"device-1"}`)
	if _, err := store.Get("profile-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("future credential metadata error = %v, want ErrNotFound", err)
	}
}

func TestSystemStoreDoesNotActivatePartialWrite(t *testing.T) {
	backend := newMemoryBackend()
	store := NewStore(backend)
	store.random = func(value []byte) error {
		for index := range value {
			value[index] = 1
		}
		return nil
	}
	backend.failAt = 2
	err := store.Set("profile-1", Credential{AccessToken: "access", RefreshToken: "refresh", DeviceID: "device"})
	if err == nil {
		t.Fatal("partial keyring write succeeded")
	}
	if len(backend.values) != 0 {
		t.Fatalf("partial keyring entries remain: %#v", backend.values)
	}
}

func TestSystemStoreRejectsMalformedMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		metadata string
	}{
		{
			name:     "unknown field",
			metadata: `{"schemaVersion":1,"deviceId":"device-1","extra":true}`,
		},
		{
			name:     "trailing document",
			metadata: `{"schemaVersion":1,"deviceId":"device-1"}{}`,
		},
		{name: "blank device", metadata: `{"schemaVersion":1,"deviceId":"  "}`},
		{name: "malformed JSON", metadata: `{"schemaVersion":1`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			backend := newMemoryBackend()
			store := NewStore(backend)
			prefix, err := accountPrefix("profile-1")
			if err != nil {
				t.Fatal(err)
			}
			backend.values[serviceName+"/"+prefix+":current"] = "generation"
			backend.values[serviceName+"/"+prefix+":generation:access"] = "access"
			backend.values[serviceName+"/"+prefix+":generation:refresh"] = "refresh"
			backend.values[serviceName+"/"+prefix+":generation:metadata"] = test.metadata

			if _, err := store.Get("profile-1"); err == nil ||
				err.Error() != "system keyring credential metadata is invalid" {
				t.Fatalf("malformed metadata error = %v", err)
			}
		})
	}
}
