package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func TestDefaultPathUsesConfigDirectory(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	path, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".kubeloop", "config", "servers.json"); path != want {
		t.Fatalf("DefaultPath() = %q, want %q", path, want)
	}
}

func TestServerProfileStoreReportsAbsolutePath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := store.Path(); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
}

func TestServerProfileStorePersistsOnlyNonSecretState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	profile := Profile{
		ID: "service-1", BaseURL: "https://gateway.example.test/",
		TunnelPath:  "/relay/tunnel",
		DisplayName: "Production", LastIdentityID: "identity-1",
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

func TestServerProfileStorePersistsSelectionAndRemoval(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
	store, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []Profile{
		{ID: "one", BaseURL: "https://one.example.test"},
		{ID: "two", BaseURL: "https://two.example.test"},
	} {
		if err := store.Upsert(profile); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.SetActive(" two "); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Snapshot().ActiveProfileID; got != "two" {
		t.Fatalf("active Profile ID after reopen = %q, want %q", got, "two")
	}

	if err := reopened.Remove(" two "); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state := reopened.Snapshot()
	if state.ActiveProfileID != "one" || len(state.Profiles) != 1 || state.Profiles[0].ID != "one" {
		t.Fatalf("state after removing active Profile = %#v", state)
	}

	if err := reopened.Remove("one"); err != nil {
		t.Fatal(err)
	}
	reopened, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	state = reopened.Snapshot()
	if state.ActiveProfileID != "" || len(state.Profiles) != 0 {
		t.Fatalf("state after removing final Profile = %#v", state)
	}
}

func TestServerProfileStoreRejectsInvalidSelectionAndRemoval(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	want := store.Snapshot()

	for _, test := range []struct {
		name   string
		mutate func() error
	}{
		{name: "empty removal", mutate: func() error { return store.Remove("  ") }},
		{name: "missing removal", mutate: func() error { return store.Remove("missing") }},
		{name: "empty selection", mutate: func() error { return store.SetActive("  ") }},
		{name: "missing selection", mutate: func() error { return store.SetActive("missing") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.mutate(); err == nil {
				t.Fatal("mutation succeeded")
			}
			got := store.Snapshot()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("failed mutation changed state: got %#v, want %#v", got, want)
			}
		})
	}
}

func TestServerProfileStoreRequiresCurrentVersionAndRejectsUnknownFields(t *testing.T) {
	if _, err := decodeState([]byte(`{"activeProfileId":"one","profiles":[]}`)); err == nil {
		t.Fatal("unversioned Server Profile store was accepted")
	}
	if _, err := decodeState(
		[]byte(
			`{"version":1,"profiles":[{"schemaVersion":1,"id":"one","baseUrl":"https://one.example.test","tunnelPath":"/tunnel"}]}`,
		),
	); err == nil {
		t.Fatal("unknown Server Profile field was accepted")
	}
}

func TestServerProfileStoreDefaultsAndValidatesTunnelPath(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
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

func TestServerProfileStorePersistsAndValidatesSOCKSPort(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test", SOCKSPort: 2080}); err != nil {
		t.Fatal(err)
	}
	if got := store.Snapshot().Profiles[0].SOCKSPort; got != 2080 {
		t.Fatalf("SOCKS port = %d", got)
	}
	for _, port := range []int{-1, 65536} {
		if err := store.Upsert(
			Profile{ID: "invalid", BaseURL: "https://invalid.example.test", SOCKSPort: port},
		); err == nil {
			t.Fatalf("invalid SOCKS port accepted: %d", port)
		}
	}
}

func TestServerProfileStoreRecoversFromValidBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "servers.json")
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
	if !recovered.RecoveredFromBackup() || len(recovered.Snapshot().Profiles) != 1 ||
		recovered.Snapshot().Profiles[0].ID != "one" {
		t.Fatalf("recovered state = %#v", recovered.Snapshot())
	}
}

func TestServerProfileStoreRejectsSecretFieldsAndUnsafeURLs(t *testing.T) {
	for _, value := range []string{
		"ftp://gateway.example.test", "https://user@gateway.example.test",
		"https://gateway.example.test?token=secret", "https://gateway.example.test/team", "not-a-url",
	} {
		if _, err := NormalizeBaseURL(value); err == nil {
			t.Fatalf("unsafe service address accepted: %q", value)
		}
	}
	path := filepath.Join(t.TempDir(), "servers.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"profiles":[],"token":"secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("profile store accepted unknown secret field")
	}
}

func TestNormalizeBaseURLAcceptsOnlyOneOrigin(t *testing.T) {
	for input, want := range map[string]string{
		"https://gateway.example.test/": "https://gateway.example.test",
		"http://gateway.example.test/":  "http://gateway.example.test",
	} {
		got, err := NormalizeBaseURL(input)
		if err != nil || got != want {
			t.Fatalf("NormalizeBaseURL(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := NormalizeBaseURL("https://gateway.example.test/team%20one/"); err == nil {
		t.Fatal("service address with a path was accepted")
	}
}

func TestServerProfileStoreReturnsDefensiveSnapshots(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{
		ID: "one", BaseURL: "https://one.example.test", DNSNamespace: "payments",
		HostAliases: []HostAlias{{Domain: "API.Example.Test.", IP: "10.0.0.8"}},
	}); err != nil {
		t.Fatal(err)
	}
	snapshot := store.Snapshot()
	snapshot.Profiles[0].BaseURL = "https://attacker.example.test"
	snapshot.Profiles[0].HostAliases[0].IP = "10.0.0.9"
	stored := store.Snapshot().Profiles[0]
	if stored.BaseURL != "https://one.example.test" || stored.HostAliases[0].IP != "10.0.0.8" {
		t.Fatal("snapshot mutated persisted profile")
	}
}

func TestServerProfileStoreSnapshotDoesNotBlockOnDiskWrite(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		if path == store.path {
			startOnce.Do(func() { close(started) })
			<-release
		}
		return utils.WriteFile(path, raw, dirMode, fileMode)
	}
	written := make(chan error, 1)
	go func() {
		written <- store.Upsert(Profile{ID: "two", BaseURL: "https://two.example.test"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("Profile store write did not start")
	}
	snapshot := make(chan State, 1)
	go func() { snapshot <- store.Snapshot() }()
	select {
	case state := <-snapshot:
		if len(state.Profiles) != 1 || state.Profiles[0].ID != "one" {
			t.Fatalf("Snapshot during write = %#v", state)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Snapshot blocked on Profile store disk write")
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if state := store.Snapshot(); len(state.Profiles) != 2 {
		t.Fatalf("committed Snapshot = %#v", state)
	}
}

func TestServerProfileStoreWriteFailurePreservesCommittedState(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{ID: "one", BaseURL: "https://one.example.test"}); err != nil {
		t.Fatal(err)
	}
	want := store.Snapshot()
	writeErr := errors.New("disk unavailable")
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		if path == store.path {
			return writeErr
		}
		return utils.WriteFile(path, raw, dirMode, fileMode)
	}
	if err := store.Upsert(Profile{ID: "two", BaseURL: "https://two.example.test"}); !errors.Is(err, writeErr) {
		t.Fatalf("Upsert error = %v", err)
	}
	if got := store.Snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("failed write changed state: got %#v, want %#v", got, want)
	}
}

func TestServerProfileStoreSerializesConcurrentWriters(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	const writers = 12
	errs := make(chan error, writers)
	var workers sync.WaitGroup
	for index := range writers {
		workers.Go(func() {
			id := fmt.Sprintf("service-%02d", index)
			errs <- store.Upsert(Profile{ID: id, BaseURL: "https://" + id + ".example.test"})
		})
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if state := store.Snapshot(); len(state.Profiles) != writers {
		t.Fatalf("concurrent writers persisted %d Profiles, want %d", len(state.Profiles), writers)
	}
}

func TestServerProfileStoreValidatesNetworkSettings(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Upsert(Profile{
		ID: "one", BaseURL: "https://one.example.test", DNSNamespace: " payments ",
		HostAliases: []HostAlias{{Domain: "API.Example.Test.", IP: "10.0.0.8"}},
	}); err != nil {
		t.Fatal(err)
	}
	stored := store.Snapshot().Profiles[0]
	if stored.DNSNamespace != "payments" || len(stored.HostAliases) != 1 ||
		stored.HostAliases[0].Domain != "api.example.test" {
		t.Fatalf("network settings = %#v", stored)
	}
	for _, invalid := range []Profile{
		{ID: "two", BaseURL: "https://two.example.test", DNSNamespace: "not/a/namespace"},
		{ID: "two", BaseURL: "https://two.example.test", HostAliases: []HostAlias{{Domain: "api.example.test", IP: "::1"}}},
		{ID: "two", BaseURL: "https://two.example.test", HostAliases: []HostAlias{{Domain: "api.example.test", IP: "10.0.0.8"}, {Domain: "API.EXAMPLE.TEST", IP: "10.0.0.9"}}},
	} {
		if err := store.Upsert(invalid); err == nil {
			t.Fatalf("invalid network settings accepted: %#v", invalid)
		}
	}
}

func TestServerProfileStoreSnapshotKeepsEmptyProfilesAsArray(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "servers.json"))
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
