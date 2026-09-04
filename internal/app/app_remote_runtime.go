package app

import (
	"crypto/tls"
	"errors"
	"path/filepath"

	"github.com/wailsapp/wails/v2/pkg/runtime"

	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func configureRemoteRuntime(
	application *App,
	layout utils.Layout,
	version string,
	developmentTLSConfig *tls.Config,
	dependencies appDependencies,
) bool {
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
		application.logError("Remote Cluster Backend unavailable: " + remoteErr.Error())
		return false
	}
	application.remote = remoteClient

	remoteFiles, fileErr := clientfiletransfer.NewManager(remoteClient, clientfiletransfer.Config{
		StatePath: filepath.Join(layout.StateDir(), "transfers.json"),
		OnEvent: func(task clientfiletransfer.Task) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "server-file-transfer:event", task)
			}
		},
	})
	if fileErr != nil {
		application.logError("file transfer manager unavailable: " + fileErr.Error())
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
		application.logError("Pod exec manager unavailable: " + execErr.Error())
	} else {
		application.remoteExecs = remoteExecs
	}

	remoteSessions, sessionErr := clientremotesession.New(remoteClient, clientremotesession.Config{})
	if sessionErr != nil {
		application.logError("Remote Session Manager unavailable: " + sessionErr.Error())
		return false
	}
	application.remoteSessions = remoteSessions
	dataPlanes, dataPlaneErr := clientdataplane.NewManager(remoteSessions, clientdataplane.Config{
		ClientVersion: version, TLSConfig: developmentTLSConfig,
		TUNStarter: NewSingboxRuntime(application.logger, application.currentLogLevel()),
		OnStatus: func(event clientdataplane.StatusEvent) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "dataplane:status", event)
			}
		},
	})
	if dataPlaneErr != nil {
		application.logError("Data Plane Manager unavailable: " + dataPlaneErr.Error())
		return false
	}
	application.dataPlanes = dataPlanes
	configureRemoteTaskManagers(application, layout, remoteClient, remoteSessions, dataPlanes)
	return true
}
