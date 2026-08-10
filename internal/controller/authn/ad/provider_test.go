package ad

import (
	"context"
	"crypto/x509"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controller/authn"
	"github.com/go-ldap/ldap/v3"
)

type fakeDirectoryClient struct {
	mu         sync.Mutex
	binds      [][2]string
	bindErr    error
	searches   []*ldap.SearchRequest
	searchFunc func(*ldap.SearchRequest) (*ldap.SearchResult, error)
	closed     bool
}

func (client *fakeDirectoryClient) Bind(username, password string) error {
	client.mu.Lock()
	defer client.mu.Unlock()
	client.binds = append(client.binds, [2]string{username, password})
	return client.bindErr
}
func (client *fakeDirectoryClient) Search(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
	client.mu.Lock()
	client.searches = append(client.searches, request)
	function := client.searchFunc
	client.mu.Unlock()
	if function == nil {
		return nil, errors.New("unexpected search")
	}
	return function(request)
}
func (client *fakeDirectoryClient) Close() {
	client.mu.Lock()
	client.closed = true
	client.mu.Unlock()
}

func TestADProviderAuthenticatesEscapesFiltersAndMapsStableIdentity(t *testing.T) {
	entry := validUserEntry()
	var capturedFilter string
	startup := &fakeDirectoryClient{}
	search := &fakeDirectoryClient{searchFunc: func(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
		capturedFilter = request.Filter
		return &ldap.SearchResult{Entries: []*ldap.Entry{entry}}, nil
	}}
	user := &fakeDirectoryClient{}
	provider := newFakeProvider(t, Config{NestedGroupDepth: 0}, startup, search, user)
	password := []byte("correct horse battery staple")
	identity, err := provider.AuthenticatePassword(context.Background(), authn.PasswordCredentials{
		Username: `ada*)(|(sAMAccountName=*))`, Password: password,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(capturedFilter, `ada*)(|`) || !strings.Contains(capturedFilter, `\2a\29\28`) {
		t.Fatalf("username was not escaped in filter %q", capturedFilter)
	}
	if identity.ProviderID != "legacy-ad" || identity.DirectoryID != "corp.example" ||
		identity.ObjectID != "33221100-5544-7766-8899-aabbccddeeff" || identity.DisplayName != "Ada Lovelace" {
		t.Fatalf("identity = %#v", identity)
	}
	if len(identity.Groups) != 1 || identity.Groups[0] != "Developers" {
		t.Fatalf("groups = %#v", identity.Groups)
	}
	if !allZero(password) {
		t.Fatal("caller password buffer was not zeroed")
	}
	if len(user.binds) != 1 || user.binds[0][0] != entry.DN || user.binds[0][1] != "correct horse battery staple" {
		t.Fatalf("user binds = %#v", user.binds)
	}
	if len(startup.binds) != 1 || startup.binds[0][0] != "CN=reader,DC=example,DC=test" {
		t.Fatalf("startup service binds = %#v", startup.binds)
	}
}

func TestADProviderExpandsNestedGroupsWithBoundedDepth(t *testing.T) {
	entry := validUserEntry()
	startup := &fakeDirectoryClient{}
	search := &fakeDirectoryClient{searchFunc: func(request *ldap.SearchRequest) (*ldap.SearchResult, error) {
		if request.Scope == ldap.ScopeWholeSubtree {
			return &ldap.SearchResult{Entries: []*ldap.Entry{entry}}, nil
		}
		if request.BaseDN == "CN=Developers,OU=Groups,DC=example,DC=test" {
			return &ldap.SearchResult{Entries: []*ldap.Entry{ldap.NewEntry(request.BaseDN, map[string][]string{
				"cn": {"Developers"}, "memberOf": {"CN=Engineering,OU=Groups,DC=example,DC=test"},
			})}}, nil
		}
		return nil, errors.New("unexpected group lookup")
	}}
	provider := newFakeProvider(t, Config{NestedGroupDepth: 1}, startup, search, &fakeDirectoryClient{})
	identity, err := provider.AuthenticatePassword(context.Background(), authn.PasswordCredentials{
		Username: "ada", Password: []byte("password"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(identity.Groups) != 2 || identity.Groups[0] != "Developers" || identity.Groups[1] != "Engineering" {
		t.Fatalf("nested groups = %#v", identity.Groups)
	}
}

func TestADProviderRejectsInvalidPasswordAndAccountStates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ldap.Entry)
		bindErr error
		want    error
	}{
		{name: "wrong password", bindErr: errors.New("LDAP invalid credentials"), want: ErrInvalidCredentials},
		{name: "disabled", mutate: func(entry *ldap.Entry) { setAttribute(entry, "userAccountControl", "514") }, want: ErrAccountUnavailable},
		{name: "locked", mutate: func(entry *ldap.Entry) { setAttribute(entry, "lockoutTime", "133700000000000000") }, want: ErrAccountUnavailable},
		{name: "password reset required", mutate: func(entry *ldap.Entry) { setAttribute(entry, "pwdLastSet", "0") }, want: ErrAccountUnavailable},
		{name: "expired", mutate: func(entry *ldap.Entry) { setAttribute(entry, "accountExpires", "116444736010000000") }, want: ErrAccountUnavailable},
		{name: "malformed status", mutate: func(entry *ldap.Entry) { setAttribute(entry, "userAccountControl", "not-a-number") }, want: ErrAccountUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validUserEntry()
			if test.mutate != nil {
				test.mutate(entry)
			}
			search := &fakeDirectoryClient{searchFunc: func(*ldap.SearchRequest) (*ldap.SearchResult, error) {
				return &ldap.SearchResult{Entries: []*ldap.Entry{entry}}, nil
			}}
			user := &fakeDirectoryClient{bindErr: test.bindErr}
			provider := newFakeProvider(t, Config{}, &fakeDirectoryClient{}, search, user)
			password := []byte("password")
			_, err := provider.AuthenticatePassword(context.Background(), authn.PasswordCredentials{Username: "ada", Password: password})
			if !errors.Is(err, test.want) {
				t.Fatalf("error = %v, want %v", err, test.want)
			}
			if !allZero(password) {
				t.Fatal("password was not zeroed on failure")
			}
		})
	}
}

func TestADConfigRejectsInsecureTransportAndUnboundedSearch(t *testing.T) {
	base := Config{ID: "ad", DirectoryID: "corp", BaseDN: "DC=example,DC=test"}
	tests := []Config{
		mergeConfig(base, Config{URL: "ldap://dc.example.test:389"}),
		mergeConfig(base, Config{URL: "ldaps://dc.example.test:636", StartTLS: true}),
		mergeConfig(base, Config{URL: "http://dc.example.test"}),
		mergeConfig(base, Config{URL: "ldaps://dc.example.test:636", UserFilter: "(sAMAccountName=*)"}),
		mergeConfig(base, Config{URL: "ldaps://127.0.0.1:636"}),
		mergeConfig(base, Config{URL: "ldaps://dc.example.test:636", NestedGroupDepth: 6}),
		mergeConfig(base, Config{URL: "ldaps://dc.example.test:636", MaxGroups: 1001}),
	}
	for _, config := range tests {
		if _, err := config.normalized(); err == nil {
			t.Fatalf("unsafe config accepted: %#v", config)
		}
	}
	withCA := mergeConfig(base, Config{URL: "ldaps://127.0.0.1:636", RootCAs: x509.NewCertPool()})
	if _, err := withCA.normalized(); err != nil {
		t.Fatalf("explicit CA IP endpoint rejected: %v", err)
	}
}

func TestADProviderFailsClosedOnDirectoryAndCertificateErrors(t *testing.T) {
	config := Config{
		ID: "ad", DirectoryID: "corp", URL: "ldaps://dc.example.test:636", BaseDN: "DC=example,DC=test",
		dial: func(context.Context, Config) (directoryClient, error) {
			return nil, errors.New("directory timeout")
		},
	}
	if _, err := New(context.Background(), config); !errors.Is(err, ErrDirectoryUnavailable) {
		t.Fatalf("startup directory error = %v", err)
	}

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	tlsConfig, err := (Config{
		ID: "ad", DirectoryID: "corp", URL: "ldaps://localhost:" + parsed.Port(),
		BaseDN: "DC=example,DC=test", RootCAs: roots,
	}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	if connection, err := dialDirectory(context.Background(), tlsConfig); err == nil {
		connection.Close()
		t.Fatal("LDAP endpoint with a hostname-mismatched certificate was accepted")
	}
}

func newFakeProvider(t *testing.T, override Config, clients ...directoryClient) *Provider {
	t.Helper()
	queue := append([]directoryClient(nil), clients...)
	var mutex sync.Mutex
	config := Config{
		ID: "legacy-ad", DisplayName: "Legacy AD", DirectoryID: "corp.example",
		URL: "ldaps://dc.example.test:636", BaseDN: "DC=example,DC=test",
		BindDN: "CN=reader,DC=example,DC=test", BindPassword: "reader-secret",
		Now: func() time.Time { return time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC) },
	}
	if override.NestedGroupDepth != 0 {
		config.NestedGroupDepth = override.NestedGroupDepth
	}
	config.dial = func(context.Context, Config) (directoryClient, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if len(queue) == 0 {
			return nil, errors.New("dial queue exhausted")
		}
		client := queue[0]
		queue = queue[1:]
		return client, nil
	}
	provider, err := New(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func validUserEntry() *ldap.Entry {
	guid := []byte{0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff}
	return &ldap.Entry{
		DN: "CN=Ada Lovelace,OU=Users,DC=example,DC=test",
		Attributes: []*ldap.EntryAttribute{
			{Name: "objectGUID", ByteValues: [][]byte{guid}},
			ldap.NewEntryAttribute("displayName", []string{"Ada Lovelace"}),
			ldap.NewEntryAttribute("mail", []string{"ada@example.test"}),
			ldap.NewEntryAttribute("memberOf", []string{"CN=Developers,OU=Groups,DC=example,DC=test"}),
			ldap.NewEntryAttribute("userAccountControl", []string{"512"}),
			ldap.NewEntryAttribute("lockoutTime", []string{"0"}),
			ldap.NewEntryAttribute("accountExpires", []string{"0"}),
			ldap.NewEntryAttribute("pwdLastSet", []string{"133700000000000000"}),
		},
	}
}

func setAttribute(entry *ldap.Entry, name, value string) {
	for _, attribute := range entry.Attributes {
		if strings.EqualFold(attribute.Name, name) {
			attribute.Values = []string{value}
			attribute.ByteValues = nil
			return
		}
	}
	entry.Attributes = append(entry.Attributes, ldap.NewEntryAttribute(name, []string{value}))
}

func allZero(value []byte) bool {
	for _, item := range value {
		if item != 0 {
			return false
		}
	}
	return true
}

func mergeConfig(base, override Config) Config {
	if override.URL != "" {
		base.URL = override.URL
	}
	base.StartTLS = override.StartTLS
	if override.UserFilter != "" {
		base.UserFilter = override.UserFilter
	}
	base.RootCAs = override.RootCAs
	base.NestedGroupDepth = override.NestedGroupDepth
	base.MaxGroups = override.MaxGroups
	return base
}
