package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

const (
	defaultTrafficInspectionLimit = 200
	maximumTrafficInspectionLimit = 500
)

type TrafficInspectionQuery struct {
	Host  string `json:"host"`
	Path  string `json:"path"`
	Limit int    `json:"limit"`
}

type TrafficInspectionResult struct {
	Enabled bool                   `json:"enabled"`
	Events  []trafficinspect.Event `json:"events"`
}

type TrafficInspectionSettings struct {
	Enabled       bool     `json:"enabled"`
	ProtobufFiles []string `json:"protobufFiles"`
}

func (a *App) GetTrafficInspectionSettings() TrafficInspectionSettings {
	settings := TrafficInspectionSettings{
		Enabled:       a != nil && a.trafficInspectionSwitch != nil && a.trafficInspectionSwitch.Enabled(),
		ProtobufFiles: make([]string, 0),
	}
	if a != nil && a.trafficInspectionProtobuf != nil {
		settings.ProtobufFiles = a.trafficInspectionProtobuf.Files()
	}
	return settings
}

func (a *App) ImportTrafficInspectionProtoDirectory() (TrafficInspectionSettings, error) {
	current := a.GetTrafficInspectionSettings()
	if a == nil || a.ctx == nil {
		return current, errors.New("application is not ready")
	}
	if a.trafficInspectionProtobuf == nil {
		return current, errors.New("protobuf schema store is unavailable")
	}
	directory, err := runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: "Select protobuf source directory",
	})
	if err != nil {
		return current, fmt.Errorf("select protobuf source directory: %w", err)
	}
	if directory == "" {
		return current, nil
	}
	if err := a.trafficInspectionProtobuf.ReplaceDirectory(a.context(), directory); err != nil {
		return current, fmt.Errorf("import protobuf schemas: %w", err)
	}
	return a.GetTrafficInspectionSettings(), nil
}

func (a *App) SetTrafficInspectionEnabled(enabled bool) (TrafficInspectionSettings, error) {
	if a == nil || a.trafficInspectionSwitch == nil {
		return TrafficInspectionSettings{}, errors.New("traffic inspection is unavailable")
	}
	a.trafficInspectionMu.Lock()
	defer a.trafficInspectionMu.Unlock()
	current := a.trafficInspectionSwitch.Enabled()
	if current == enabled {
		return a.GetTrafficInspectionSettings(), nil
	}
	if a.trafficInspectionReady == nil || !a.trafficInspectionReady() {
		return a.GetTrafficInspectionSettings(), errors.New(
			"virtual network service must be running to change traffic inspection",
		)
	}
	if enabled {
		if err := a.ensureTrafficInspectionTrust(a.context()); err != nil {
			return a.GetTrafficInspectionSettings(), fmt.Errorf("enable traffic inspection: %w", err)
		}
	}
	if a.trafficInspectionSettings == nil {
		return a.GetTrafficInspectionSettings(), errors.New("traffic inspection settings store is unavailable")
	}
	if err := a.trafficInspectionSettings.Save(trafficinspect.Settings{Enabled: enabled}); err != nil {
		return a.GetTrafficInspectionSettings(), err
	}
	a.trafficInspectionSwitch.SetEnabled(enabled)
	return a.GetTrafficInspectionSettings(), nil
}

// TrafficInspectionEvents returns the newest decoded application events.
// The persistent JSONL file is deliberately not queried by the UI.
func (a *App) TrafficInspectionEvents(query TrafficInspectionQuery) TrafficInspectionResult {
	result := TrafficInspectionResult{Events: make([]trafficinspect.Event, 0)}
	if a.trafficInspectionEvents == nil || a.trafficInspectionSwitch == nil {
		return result
	}
	result.Enabled = a.trafficInspectionSwitch.Enabled()
	if !result.Enabled {
		return result
	}
	host := strings.ToLower(strings.TrimSpace(query.Host))
	path := strings.ToLower(strings.TrimSpace(query.Path))
	limit := query.Limit
	if limit <= 0 {
		limit = defaultTrafficInspectionLimit
	} else if limit > maximumTrafficInspectionLimit {
		limit = maximumTrafficInspectionLimit
	}
	events := a.trafficInspectionEvents.Snapshot()
	for index := len(events) - 1; index >= 0 && len(result.Events) < limit; index-- {
		event := events[index]
		if !trafficEventMatches(event, host, path) {
			continue
		}
		result.Events = append(result.Events, event)
	}
	return result
}

func trafficEventMatches(event trafficinspect.Event, host, path string) bool {
	hostValue := event.Destination
	pathValue := ""
	if event.HTTP != nil {
		hostValue += " " + event.HTTP.Host
		pathValue = event.HTTP.Path
	}
	if event.GRPC != nil {
		pathValue = event.GRPC.Path
	}
	return strings.Contains(strings.ToLower(hostValue), host) &&
		strings.Contains(strings.ToLower(pathValue), path)
}
