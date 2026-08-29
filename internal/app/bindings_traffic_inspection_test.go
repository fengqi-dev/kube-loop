package app

import (
	"path/filepath"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func TestTrafficInspectionEventsDisabled(t *testing.T) {
	result := (&App{}).TrafficInspectionEvents(TrafficInspectionQuery{})
	if result.Enabled || result.Events == nil || len(result.Events) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestTrafficInspectionEventsReturnsNewestMatchingEvents(t *testing.T) {
	enabled := testTrafficInspectionEnabled(t, true)
	events := trafficinspect.NewEventBuffer(4)
	events.Append(trafficinspect.Event{
		ID: "old", Destination: "10.0.0.1:443",
		HTTP: &trafficinspect.HTTPEvent{Host: "api.example", Path: "/old"},
	})
	events.Append(trafficinspect.Event{
		ID: "new", Destination: "10.0.0.1:443",
		HTTP: &trafficinspect.HTTPEvent{Host: "api.example", Path: "/users"},
	})
	application := &App{trafficInspectionEnabled: enabled, trafficInspectionEvents: events}
	result := application.TrafficInspectionEvents(TrafficInspectionQuery{
		Host: "API.EXAMPLE", Path: "/users", Limit: 1,
	})
	if !result.Enabled || len(result.Events) != 1 || result.Events[0].ID != "new" {
		t.Fatalf("traffic inspection result = %#v", result)
	}
}

func TestSetTrafficInspectionEnabledPersistsAndAppliesImmediately(t *testing.T) {
	directory := t.TempDir()
	settingsStore, err := trafficinspect.NewSettingsStore(filepath.Join(directory, "traffic-inspection.json"))
	if err != nil {
		t.Fatal(err)
	}
	enabled := testTrafficInspectionEnabled(t, false)
	trustStore := &recordingTrustStore{}
	application := &App{
		trafficInspectionEnabled:  enabled,
		trafficInspectionSettings: settingsStore,
		trafficInspectionReady:    func() bool { return true },
		trafficInspectionCAPath:   filepath.Join(directory, "traffic-inspection-ca.pem"),
		trafficInspectionTrust:    trustStore,
	}

	settings, err := application.SetTrafficInspectionEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || !enabled.Load() || trustStore.installCalls != 1 {
		t.Fatalf(
			"enabled settings = %#v, switch = %t, installs = %d",
			settings,
			enabled.Load(),
			trustStore.installCalls,
		)
	}
	settings, err = application.SetTrafficInspectionEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || enabled.Load() {
		t.Fatalf("disabled settings = %#v, enabled = %t", settings, enabled.Load())
	}
	persisted, err := settingsStore.Load(trafficinspect.Settings{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Enabled {
		t.Fatal("disabled setting was not persisted")
	}
}

func TestSetTrafficInspectionEnabledRequiresRunningVirtualNetworkService(t *testing.T) {
	directory := t.TempDir()
	settingsStore, err := trafficinspect.NewSettingsStore(filepath.Join(directory, "traffic-inspection.json"))
	if err != nil {
		t.Fatal(err)
	}
	enabled := testTrafficInspectionEnabled(t, false)
	application := &App{
		trafficInspectionEnabled:  enabled,
		trafficInspectionSettings: settingsStore,
		trafficInspectionReady:    func() bool { return false },
	}
	settings, err := application.SetTrafficInspectionEnabled(true)
	if err == nil {
		t.Fatal("stopped virtual network service allowed traffic inspection change")
	}
	if settings.Enabled || enabled.Load() {
		t.Fatalf("settings = %#v, enabled = %t", settings, enabled.Load())
	}
}
