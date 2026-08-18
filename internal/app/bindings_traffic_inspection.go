package app

import (
	"errors"
	"fmt"
	"strings"

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
	Enabled bool `json:"enabled"`
}

func (a *App) GetTrafficInspectionSettings() TrafficInspectionSettings {
	return TrafficInspectionSettings{Enabled: a != nil && a.trafficInspectionSwitch != nil && a.trafficInspectionSwitch.Enabled()}
}

func (a *App) SetTrafficInspectionEnabled(enabled bool) (TrafficInspectionSettings, error) {
	if a == nil || a.trafficInspectionSwitch == nil {
		return TrafficInspectionSettings{}, errors.New("traffic inspection is unavailable")
	}
	a.trafficInspectionMu.Lock()
	defer a.trafficInspectionMu.Unlock()
	current := a.trafficInspectionSwitch.Enabled()
	if current == enabled {
		return TrafficInspectionSettings{Enabled: current}, nil
	}
	if a.trafficInspectionReady == nil || !a.trafficInspectionReady() {
		return TrafficInspectionSettings{Enabled: current}, errors.New("virtual network service must be running to change traffic inspection")
	}
	if enabled {
		if err := a.ensureTrafficInspectionTrust(a.context()); err != nil {
			return TrafficInspectionSettings{Enabled: current}, fmt.Errorf("enable traffic inspection: %w", err)
		}
	}
	if a.trafficInspectionSettings == nil {
		return TrafficInspectionSettings{Enabled: current}, errors.New("traffic inspection settings store is unavailable")
	}
	if err := a.trafficInspectionSettings.Save(trafficinspect.Settings{Enabled: enabled}); err != nil {
		return TrafficInspectionSettings{Enabled: current}, err
	}
	a.trafficInspectionSwitch.SetEnabled(enabled)
	return TrafficInspectionSettings{Enabled: enabled}, nil
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
