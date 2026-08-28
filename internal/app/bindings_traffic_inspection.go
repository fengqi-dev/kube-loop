package app

import (
	"errors"
	"fmt"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
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
		Enabled:       a != nil && a.trafficInspectionEnabled != nil && a.trafficInspectionEnabled.Load(),
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
	if a == nil || a.trafficInspectionEnabled == nil {
		return TrafficInspectionSettings{}, errors.New("traffic inspection is unavailable")
	}
	a.trafficInspectionMu.Lock()
	defer a.trafficInspectionMu.Unlock()
	current := a.trafficInspectionEnabled.Load()
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
	a.trafficInspectionEnabled.Store(enabled)
	return a.GetTrafficInspectionSettings(), nil
}

// TrafficInspectionEvents preserves the frontend API shape. Events are no
// longer buffered by the application after inspection sinks were removed.
func (a *App) TrafficInspectionEvents(query TrafficInspectionQuery) TrafficInspectionResult {
	_ = query
	result := TrafficInspectionResult{Events: make([]trafficinspect.Event, 0)}
	if a != nil && a.trafficInspectionEnabled != nil {
		result.Enabled = a.trafficInspectionEnabled.Load()
	}
	return result
}
