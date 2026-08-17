package credentials

import (
	"encoding/json"
	"errors"
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
	if got.AccessToken != credential.AccessToken || got.RefreshToken != credential.RefreshToken || got.DeviceID != credential.DeviceID ||
		got.IdentityID != credential.IdentityID || got.UserName != credential.UserName {
		t.Fatalf("credential = %#v", got)
	}
	prefix, err := accountPrefix("profile-1")
	if err != nil {
		t.Fatal(err)
	}
	generation := backend.values[serviceName+"/"+prefix+":current"]
	var details metadata
	if err := json.Unmarshal([]byte(backend.values[serviceName+"/"+prefix+":"+generation+":metadata"]), &details); err != nil {
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
