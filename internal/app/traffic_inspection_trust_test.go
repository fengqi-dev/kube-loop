package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func TestInstallTrafficInspectionTrustFollowsFeatureSwitch(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inspection-ca.pem")
	store := &recordingTrustStore{}
	application := &App{
		trafficInspectionSwitch: testTrafficInspectionSwitch(t, true),
		trafficInspectionCAPath: path,
		trafficInspectionTrust:  store,
	}
	if err := application.installTrafficInspectionTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.installCalls != 1 || store.fingerprint == "" {
		t.Fatalf("trust install calls = %d, fingerprint = %q", store.installCalls, store.fingerprint)
	}

	disabled := &App{trafficInspectionCAPath: path, trafficInspectionTrust: store}
	if err := disabled.installTrafficInspectionTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.installCalls != 1 {
		t.Fatalf("disabled trust install calls = %d, want 1", store.installCalls)
	}
}

func TestPendingTrafficInspectionCertificate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inspection-ca.pem")
	store := &recordingTrustStore{}
	application := &App{
		trafficInspectionSwitch: testTrafficInspectionSwitch(t, true),
		trafficInspectionCAPath: path,
		trafficInspectionTrust:  store,
	}
	certificatePEM, err := application.pendingTrafficInspectionCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(certificatePEM) == 0 {
		t.Fatal("pendingTrafficInspectionCertificate() returned no certificate")
	}

	store.installed = true
	certificatePEM, err = application.pendingTrafficInspectionCertificate(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(certificatePEM) != 0 {
		t.Fatal("pendingTrafficInspectionCertificate() returned an already trusted certificate")
	}
}

func testTrafficInspectionSwitch(t *testing.T, enabled bool) *trafficinspect.SwitchableSink {
	t.Helper()
	destination, err := trafficinspect.NewRingBufferSink(1)
	if err != nil {
		t.Fatal(err)
	}
	switchable, err := trafficinspect.NewSwitchableSink(destination, enabled)
	if err != nil {
		t.Fatal(err)
	}
	return switchable
}

func TestUninstallTrafficInspectionTrustRemovesExistingAuthorityEvenWhenDisabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inspection-ca.pem")
	if _, err := trafficinspect.LoadOrCreateAuthority(path); err != nil {
		t.Fatal(err)
	}
	store := &recordingTrustStore{}
	application := &App{trafficInspectionCAPath: path, trafficInspectionTrust: store}
	if err := application.uninstallTrafficInspectionTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.uninstallCalls != 1 || store.fingerprint == "" {
		t.Fatalf("trust uninstall calls = %d, fingerprint = %q", store.uninstallCalls, store.fingerprint)
	}
}

func TestUninstallTrafficInspectionTrustSkipsMissingAuthority(t *testing.T) {
	store := &recordingTrustStore{}
	application := &App{
		trafficInspectionCAPath: filepath.Join(t.TempDir(), "missing.pem"),
		trafficInspectionTrust:  store,
	}
	if err := application.uninstallTrafficInspectionTrust(t.Context()); err != nil {
		t.Fatal(err)
	}
	if store.uninstallCalls != 0 {
		t.Fatalf("trust uninstall calls = %d, want 0", store.uninstallCalls)
	}
}

type recordingTrustStore struct {
	installed      bool
	installCalls   int
	uninstallCalls int
	fingerprint    string
}

func (s *recordingTrustStore) Status(context.Context, *trafficinspect.Authority) (trafficinspect.TrustStatus, error) {
	return trafficinspect.TrustStatus{Installed: s.installed}, nil
}

func (s *recordingTrustStore) Install(_ context.Context, authority *trafficinspect.Authority) error {
	s.installCalls++
	s.fingerprint = authority.FingerprintSHA256()
	return nil
}

func (s *recordingTrustStore) Uninstall(_ context.Context, authority *trafficinspect.Authority) error {
	s.uninstallCalls++
	s.fingerprint = authority.FingerprintSHA256()
	return nil
}
