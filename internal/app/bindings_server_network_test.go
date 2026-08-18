package app

import (
	"path/filepath"
	"testing"

	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func TestServerNetworkSettingsPersistWithoutActiveDataPlane(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{
		ID: "service-1", BaseURL: "https://gateway.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}

	settings, err := application.SetServerDNSNamespace("service-1", "development")
	if err != nil {
		t.Fatal(err)
	}
	if settings.DNSNamespace != "development" {
		t.Fatalf("DNS namespace = %q", settings.DNSNamespace)
	}
	settings, err = application.SetServerSOCKSPort("service-1", 2080)
	if err != nil {
		t.Fatal(err)
	}
	if settings.SOCKSPort != 2080 {
		t.Fatalf("SOCKS port = %d", settings.SOCKSPort)
	}
	settings, err = application.SetServerHostAliases("service-1", []clientprofile.HostAlias{
		{Domain: "API.Development.SVC.Cluster.Local.", IP: "10.96.0.20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings.HostAliases) != 1 || settings.HostAliases[0].Domain != "api.development.svc.cluster.local" {
		t.Fatalf("host aliases = %#v", settings.HostAliases)
	}

	stored, err := application.GetServerNetworkSettings("service-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DNSNamespace != "development" || stored.SOCKSPort != 2080 || len(stored.HostAliases) != 1 {
		t.Fatalf("stored settings = %#v", stored)
	}
}

func TestServerNetworkSettingsRejectInvalidValues(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{
		ID: "service-1", BaseURL: "https://gateway.example.test",
	}); err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}

	if _, err := application.SetServerDNSNamespace("service-1", "bad/namespace"); err == nil {
		t.Fatal("invalid DNS namespace was accepted")
	}
	if _, err := application.SetServerHostAliases("service-1", []clientprofile.HostAlias{
		{Domain: "api.example", IP: "2001:db8::1"},
	}); err == nil {
		t.Fatal("IPv6 host alias was accepted")
	}
	if _, err := application.SetServerSOCKSPort("service-1", 0); err == nil {
		t.Fatal("invalid SOCKS port was accepted")
	}
	stored, err := application.GetServerNetworkSettings("service-1")
	if err != nil {
		t.Fatal(err)
	}
	if stored.DNSNamespace != "" || len(stored.HostAliases) != 0 {
		t.Fatalf("invalid settings changed the profile: %#v", stored)
	}
}

func TestServerDataPlaneDiagnosticsReportUnavailableManager(t *testing.T) {
	application := &App{}
	if _, err := application.ServerDataPlaneMetrics("service-1"); err == nil {
		t.Fatal("metrics succeeded without Data Plane manager")
	}
	if _, err := application.ServerDataPlaneLogs("service-1"); err == nil {
		t.Fatal("logs succeeded without Data Plane manager")
	}
	if _, err := application.GetServerSingBoxConfig("service-1"); err == nil {
		t.Fatal("config succeeded without Data Plane manager")
	}
}

func TestServerNetworkSettingsRejectUnknownProfile(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}
	if _, err := application.GetServerNetworkSettings("missing"); err == nil {
		t.Fatal("unknown profile returned network settings")
	}
}

func TestServerNetworkSettingsReturnsDefensiveAliases(t *testing.T) {
	profileStore, err := clientprofile.Open(filepath.Join(t.TempDir(), "servers.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := profileStore.Upsert(clientprofile.Profile{
		ID: "service-1", BaseURL: "https://gateway.example.test",
		HostAliases: []clientprofile.HostAlias{{Domain: "api.example.test", IP: "10.0.0.8"}},
	}); err != nil {
		t.Fatal(err)
	}
	application := &App{profiles: profileStore}
	settings, err := application.GetServerNetworkSettings("service-1")
	if err != nil {
		t.Fatal(err)
	}
	settings.HostAliases[0].Domain = "mutated.example.test"
	stored, err := application.GetServerNetworkSettings("service-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(stored.HostAliases) != 1 || stored.HostAliases[0].Domain != "api.example.test" {
		t.Fatalf("returned aliases mutated persisted state: %#v", stored.HostAliases)
	}
}
