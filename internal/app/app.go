package app

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	"github.com/fengqi-dev/kube-loop/internal/authconfig"
	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
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
	trafficInspection, trafficInspectionEvents, trafficInspectionSwitch := newTrafficInspection(version, profilePath)
	return newApp(version, embeddedHelperFiles, appDependencies{
		profilePath:             profilePath,
		trafficInspection:       trafficInspection,
		trafficInspectionEvents: trafficInspectionEvents,
		trafficInspectionSwitch: trafficInspectionSwitch,
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
			updater:                 &update.Checker{CurrentVersion: version},
			trafficInspectionEvents: dependencies.trafficInspectionEvents,
			trafficInspectionSwitch: dependencies.trafficInspectionSwitch,
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
	transferStatePath := filepath.Join(layout.StateDir(), "transfers.json")
	trafficInspectionSettingsPath := filepath.Join(layout.ConfigDir(), "traffic-inspection.json")
	trafficInspectionSettings, trafficInspectionSettingsErr := trafficinspect.NewSettingsStore(
		trafficInspectionSettingsPath,
	)
	if trafficInspectionSettingsErr == nil && dependencies.trafficInspectionSwitch != nil {
		settings, loadErr := trafficInspectionSettings.Load(trafficinspect.Settings{
			Enabled: dependencies.trafficInspection.Enabled,
		})
		if loadErr != nil {
			trafficInspectionSettingsErr = loadErr
		} else {
			dependencies.trafficInspection.Enabled = settings.Enabled
			dependencies.trafficInspectionSwitch.SetEnabled(settings.Enabled)
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
		credentialStore = credentials.NewSystemStoreForClient(version, authconfig.DesktopClientID)
	}
	application := &App{
		profiles: profileStore,
		discovery: clientdiscovery.New(clientdiscovery.Config{
			ClientVersion: version,
			HTTPClient:    dependencies.httpClient,
		}),
		credentials:               credentialStore,
		updater:                   &update.Checker{CurrentVersion: version},
		trafficInspectionEvents:   dependencies.trafficInspectionEvents,
		trafficInspectionSwitch:   dependencies.trafficInspectionSwitch,
		trafficInspectionSettings: trafficInspectionSettings,
		trafficInspectionProtobuf: trafficInspectionProtobuf,
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
	if closer, ok := dependencies.trafficInspection.Sink.(io.Closer); ok {
		application.trafficInspectionOutput = closer
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
	application.auth = clientauth.New(clientauth.Config{
		HTTPClient: dependencies.httpClient,
		OpenBrowser: func(target string) error {
			if application.ctx == nil {
				return errors.New("application is not ready")
			}
			runtime.BrowserOpenURL(application.ctx, target)
			return nil
		},
		BrowserCallback: func() {
			if application.ctx == nil {
				return
			}
			runtime.WindowUnminimise(application.ctx)
			runtime.Show(application.ctx)
		},
	})
	remoteClient, remoteErr := clientremote.New(
		application.credentials, application.auth, clientremote.Config{HTTPClient: dependencies.httpClient},
	)
	if remoteErr != nil {
		application.appendLog("ERROR", "Remote Cluster Backend unavailable: "+remoteErr.Error())
		return application
	}
	application.remote = remoteClient

	remoteFiles, fileErr := clientfiletransfer.NewManager(remoteClient, clientfiletransfer.Config{
		StatePath: transferStatePath,
		OnEvent: func(task clientfiletransfer.Task) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "server-file-transfer:event", task)
			}
		},
	})
	if fileErr != nil {
		application.appendLog("ERROR", "file transfer manager unavailable: "+fileErr.Error())
	} else {
		application.remoteFiles = remoteFiles
	}

	remoteExecs, execErr := clientexec.NewManager(remoteClient, clientexec.ManagerConfig{
		OnEvent: func(event clientexec.Event) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "server-exec:event", event)
			}
		},
	})
	if execErr != nil {
		application.appendLog("ERROR", "Pod exec manager unavailable: "+execErr.Error())
	} else {
		application.remoteExecs = remoteExecs
	}

	remoteSessions, sessionErr := clientremotesession.New(remoteClient, clientremotesession.Config{})
	if sessionErr != nil {
		application.appendLog("ERROR", "Remote Session Manager unavailable: "+sessionErr.Error())
		return application
	}
	application.remoteSessions = remoteSessions
	dataPlanes, dataPlaneErr := clientdataplane.NewManager(remoteSessions, clientdataplane.Config{
		ClientVersion: version, TLSConfig: developmentTLSConfig,
		TUNStarter: NewSingboxRuntime(
			application.appendLog,
			application.installTrafficInspectionTrust,
		),
		TrafficInspection: dependencies.trafficInspection,
		OnStatus: func(event clientdataplane.StatusEvent) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "dataplane:status", event)
			}
		},
	})
	if dataPlaneErr != nil {
		application.appendLog("ERROR", "Data Plane Manager unavailable: "+dataPlaneErr.Error())
		return application
	}
	application.dataPlanes = dataPlanes
	configureRemoteTaskManagers(application, layout, remoteClient, remoteSessions, dataPlanes)

	configureMCP(application, layout, version)
	return application
}
