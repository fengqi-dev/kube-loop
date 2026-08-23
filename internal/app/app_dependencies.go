package app

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

type appDependencies struct {
	profilePath             string
	credentialStore         credentials.Store
	httpClient              *http.Client
	trafficInspection       clientdataplane.TrafficInspectionConfig
	trafficInspectionEvents *trafficinspect.RingBufferSink
	trafficInspectionSwitch *trafficinspect.SwitchableSink
}

func appUserLayout(version, profilePath string) (userpaths.Layout, error) {
	profilePath = strings.TrimSpace(profilePath)
	var layout userpaths.Layout
	var err error
	if profilePath == "" {
		layout, err = userpaths.ForVersion(version)
	} else {
		root := filepath.Dir(profilePath)
		if filepath.Base(root) == "config" {
			root = filepath.Dir(root)
		}
		layout, err = userpaths.New(root)
	}
	if err != nil {
		return userpaths.Layout{}, err
	}
	if err := layout.Ensure(); err != nil {
		return userpaths.Layout{}, fmt.Errorf("initialize KubeLoop user directories: %w", err)
	}
	return layout, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
