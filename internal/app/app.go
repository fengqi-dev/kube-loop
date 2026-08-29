package app

import (
	"context"
	"crypto/tls"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/supervisor"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
	"github.com/fengqi-dev/kube-loop/internal/update"
)

// NewApp is the desktop composition root. It deliberately constructs no
// kubeconfig or Kubernetes client: all cluster operations
// are performed remotely through the configured Gateway service.
func NewApp(version string, embeddedHelperFiles fs.FS) *App {
	profilePath := strings.TrimSpace(os.Getenv("KUBELOOP_PROFILE_PATH"))
	trafficInspection, trafficInspectionEnabled := newTrafficInspection()
	trafficInspectionEvents := trafficinspect.NewEventBuffer(2_000)
	return newApp(version, embeddedHelperFiles, appDependencies{
		profilePath:              profilePath,
		trafficInspection:        trafficInspection,
		trafficInspectionEnabled: trafficInspectionEnabled,
		trafficInspectionEvents:  trafficInspectionEvents,
	})
}

func newApp(version string, embeddedHelperFiles fs.FS, dependencies appDependencies) *App {
	registerEmbeddedHelpers(embeddedHelperFiles)
	if version != "" {
		helper.Version = version
		supervisor.Version = version
	}
	var developmentTLSConfig *tls.Config
	var developmentTLSErr error
	if dependencies.httpClient == nil && helper.IsDevBuild() {
		dependencies.httpClient, developmentTLSConfig, developmentTLSErr = developmentGatewayHTTPClient(
			embeddedHelperFiles,
		)
	}

	layout, layoutErr := appUserLayout(version, dependencies.profilePath)
	if layoutErr != nil {
		application := &App{
			updater:                  &update.Checker{CurrentVersion: version},
			trafficInspectionEnabled: dependencies.trafficInspectionEnabled,
			updateState: update.Info{
				CurrentVersion: version,
				URL:            releaseURL,
			},
		}
		application.appendLog("ERROR", "KubeLoop user layout unavailable: "+layoutErr.Error())
		return application
	}
	profilePath := strings.TrimSpace(dependencies.profilePath)
	if profilePath == "" {
		profilePath = filepath.Join(layout.ConfigDir(), "servers.json")
	}
	profileStore, profileErr := clientprofile.Open(profilePath)
	trafficInspectionSettingsPath := filepath.Join(layout.ConfigDir(), "traffic-inspection.json")
	trafficInspectionSettings, trafficInspectionSettingsErr := trafficinspect.NewSettingsStore(
		trafficInspectionSettingsPath,
	)
	if trafficInspectionSettingsErr == nil && dependencies.trafficInspectionEnabled != nil {
		settings, loadErr := trafficInspectionSettings.Load(trafficinspect.Settings{
			Enabled: dependencies.trafficInspection.Enabled,
		})
		if loadErr != nil {
			trafficInspectionSettingsErr = loadErr
		} else {
			dependencies.trafficInspection.Enabled = settings.Enabled
			dependencies.trafficInspectionEnabled.Store(settings.Enabled)
		}
	}
	trafficInspectionProtobufPath := filepath.Join(layout.DataDir(), "traffic-inspection", "protobuf.json")
	trafficInspectionProtobuf, trafficInspectionProtobufErr := trafficinspect.NewProtobufSchemaStore(
		trafficInspectionProtobufPath,
		dependencies.trafficInspection.Protobuf,
	)
	if trafficInspectionProtobufErr == nil {
		trafficInspectionProtobufErr = trafficInspectionProtobuf.Load(context.Background())
	}

	credentialStore := dependencies.credentialStore
	if credentialStore == nil {
		credentialStore = credentials.NewSystemStoreForClient(version, auth.DesktopClientID)
	}
	application := &App{
		profiles: profileStore,
		discovery: clientdiscovery.New(clientdiscovery.Config{
			ClientVersion: version,
			HTTPClient:    dependencies.httpClient,
		}),
		credentials:               credentialStore,
		updater:                   &update.Checker{CurrentVersion: version},
		trafficInspectionEnabled:  dependencies.trafficInspectionEnabled,
		trafficInspectionSettings: trafficInspectionSettings,
		trafficInspectionProtobuf: trafficInspectionProtobuf,
		trafficInspectionEvents:   dependencies.trafficInspectionEvents,
		trafficInspectionCAPath: firstNonEmpty(
			dependencies.trafficInspection.AuthorityPath,
			filepath.Join(layout.SecretsDir(), "inspection-ca.pem"),
		),
		trafficInspectionTrust: trafficinspect.NewSystemTrustStore(),
		updateState: update.Info{
			CurrentVersion: version,
			URL:            releaseURL,
		},
	}
	if application.trafficInspectionEvents != nil {
		dependencies.trafficInspection.OnEvent = application.trafficInspectionEvents.Append
	}
	if trafficInspectionSettingsErr != nil {
		application.appendLog("ERROR", "Traffic inspection settings unavailable: "+trafficInspectionSettingsErr.Error())
	}
	if trafficInspectionProtobufErr != nil {
		application.appendLog(
			"ERROR",
			"Traffic inspection protobuf schemas unavailable: "+trafficInspectionProtobufErr.Error(),
		)
	}
	application.trafficInspectionReady = func() bool {
		return helper.GetStatus(application.context()).Running
	}
	switch {
	case profileErr != nil:
		application.appendLog("ERROR", "Server Profile store unavailable: "+profileErr.Error())
	case profileStore.RecoveredFromBackup():
		application.appendLog("WARN", "Server Profile store recovered from backup")
	default:
		application.appendLog("INFO", "Server Profile store loaded")
	}
	if developmentTLSErr != nil {
		application.appendLog("WARN", "Development Gateway CA unavailable: "+developmentTLSErr.Error())
	}
	if !configureRemoteRuntime(application, layout, version, developmentTLSConfig, dependencies) {
		return application
	}

	configureMCP(application, layout, version)
	return application
}
