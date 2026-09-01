package app

import (
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
	"github.com/fengqi-dev/kube-loop/internal/update"
)

// NewApp is the desktop composition root. It deliberately constructs no
// kubeconfig or Kubernetes client: all cluster operations
// are performed remotely through the configured Gateway service.
func NewApp(version string, embeddedHelperFiles fs.FS) *App {
	profilePath := strings.TrimSpace(os.Getenv("KUBELOOP_PROFILE_PATH"))
	return newApp(version, embeddedHelperFiles, appDependencies{
		profilePath: profilePath,
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
			updater: &update.Checker{CurrentVersion: version},
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
		credentials: credentialStore,
		updater:     &update.Checker{CurrentVersion: version},
		updateState: update.Info{
			CurrentVersion: version,
			URL:            releaseURL,
		},
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
