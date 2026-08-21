package app

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func TestTrafficInspectionEventsDisabled(t *testing.T) {
	result := (&App{}).TrafficInspectionEvents(TrafficInspectionQuery{})
	if result.Enabled || result.Events == nil || len(result.Events) != 0 {
		t.Fatalf("result = %#v", result)
	}
}

func TestSetTrafficInspectionEnabledPersistsAndAppliesImmediately(t *testing.T) {
	directory := t.TempDir()
	settingsStore, err := trafficinspect.NewSettingsStore(filepath.Join(directory, "traffic-inspection.json"))
	if err != nil {
		t.Fatal(err)
	}
	switchable := testTrafficInspectionSwitch(t, false)
	trustStore := &recordingTrustStore{}
	application := &App{
		trafficInspectionSwitch:   switchable,
		trafficInspectionSettings: settingsStore,
		trafficInspectionReady:    func() bool { return true },
		trafficInspectionCAPath:   filepath.Join(directory, "traffic-inspection-ca.pem"),
		trafficInspectionTrust:    trustStore,
	}

	settings, err := application.SetTrafficInspectionEnabled(true)
	if err != nil {
		t.Fatal(err)
	}
	if !settings.Enabled || !switchable.Enabled() || trustStore.installCalls != 1 {
		t.Fatalf(
			"enabled settings = %#v, switch = %t, installs = %d",
			settings,
			switchable.Enabled(),
			trustStore.installCalls,
		)
	}
	settings, err = application.SetTrafficInspectionEnabled(false)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Enabled || switchable.Enabled() {
		t.Fatalf("disabled settings = %#v, switch = %t", settings, switchable.Enabled())
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
	switchable := testTrafficInspectionSwitch(t, false)
	application := &App{
		trafficInspectionSwitch:   switchable,
		trafficInspectionSettings: settingsStore,
		trafficInspectionReady:    func() bool { return false },
	}
	settings, err := application.SetTrafficInspectionEnabled(true)
	if err == nil {
		t.Fatal("stopped virtual network service allowed traffic inspection change")
	}
	if settings.Enabled || switchable.Enabled() {
		t.Fatalf("settings = %#v, switch = %t", settings, switchable.Enabled())
	}
}

func TestTrafficInspectionEventsFiltersAndReturnsNewestFirst(t *testing.T) {
	sink, err := trafficinspect.NewRingBufferSink(10)
	if err != nil {
		t.Fatal(err)
	}
	events := []trafficinspect.Event{
		{
			ID:          "one",
			Timestamp:   time.Unix(1, 0),
			Destination: "api.example.test:443",
			HTTP:        &trafficinspect.HTTPEvent{Host: "api.example.test", Path: "/v1/users"},
		},
		{
			ID:          "two",
			Timestamp:   time.Unix(2, 0),
			Destination: "grpc.example.test:443",
			GRPC:        &trafficinspect.GRPCEvent{Path: "/demo.Echo/Say"},
		},
		{
			ID:          "three",
			Timestamp:   time.Unix(3, 0),
			Destination: "api.example.test:443",
			HTTP:        &trafficinspect.HTTPEvent{Host: "api.example.test", Path: "/v1/users/3"},
		},
	}
	for _, event := range events {
		if err := sink.Emit(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	application := &App{
		trafficInspectionEvents: sink,
		trafficInspectionSwitch: testTrafficInspectionSwitch(t, true),
	}

	result := application.TrafficInspectionEvents(
		TrafficInspectionQuery{Host: "API.EXAMPLE", Path: "/v1/users", Limit: 1},
	)
	if !result.Enabled || len(result.Events) != 1 || result.Events[0].ID != "three" {
		t.Fatalf("filtered result = %#v", result)
	}

	result = application.TrafficInspectionEvents(TrafficInspectionQuery{Host: "grpc.example", Path: "echo/say"})
	if len(result.Events) != 1 || result.Events[0].ID != "two" {
		t.Fatalf("gRPC result = %#v", result)
	}
}
