package app

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

type appDependencies struct {
	profilePath              string
	credentialStore          credentials.Store
	httpClient               *http.Client
	trafficInspection        clientdataplane.TrafficInspectionConfig
	trafficInspectionEnabled *atomic.Bool
	trafficInspectionEvents  *trafficinspect.EventBuffer
}

func appUserLayout(version, profilePath string) (utils.Layout, error) {
	profilePath = strings.TrimSpace(profilePath)
	var layout utils.Layout
	var err error
	if profilePath == "" {
		layout, err = utils.ForVersion(version)
	} else {
		root := filepath.Dir(profilePath)
		if filepath.Base(root) == "config" {
			root = filepath.Dir(root)
		}
		layout, err = utils.New(root)
	}
	if err != nil {
		return utils.Layout{}, err
	}
	if err := layout.Ensure(); err != nil {
		return utils.Layout{}, fmt.Errorf("initialize KubeLoop user directories: %w", err)
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
