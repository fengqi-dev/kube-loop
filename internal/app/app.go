package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"path/filepath"
	goruntime "runtime"
	"sync"
	"time"

	clientauth "github.com/fengqi-dev/kube-loop/internal/clientv2/auth"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/clientv2/dataplane"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/clientv2/discovery"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/clientv2/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/clientv2/exec"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/clientv2/filetransfer"
	clientmigration "github.com/fengqi-dev/kube-loop/internal/clientv2/migration"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/clientv2/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/clientv2/podssh"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/clientv2/portforward"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/powerwatch"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/clientv2/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/clientv2/remotesession"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/mcp"
	"github.com/fengqi-dev/kube-loop/internal/update"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx                   context.Context
	profiles              *clientprofile.Store
	discovery             *clientdiscovery.Client
	auth                  *clientauth.Client
	remote                *clientremote.Client
	remoteSessions        *clientremotesession.Manager
	dataPlanes            *clientdataplane.Manager
	remoteExecs           *clientexec.Manager
	remoteFiles           *clientfiletransfer.Manager
	remoteSSH             *clientpodssh.Manager
	remoteForwards        *clientportforward.Manager
	remoteExchanges       *clientexchange.Manager
	remoteMirrors         *clientmirror.Manager
	remotePreviews        *clientpreview.Manager
	credentials           credentials.Store
	migration             clientmigration.Status
	mcp                   *mcp.Controller
	updater               *update.Checker
	once                  sync.Once
	updateMu              sync.RWMutex
	updateCheck           sync.Mutex
	updateState           update.Info
	inventoryWatchMu      sync.Mutex
	inventoryWatchProfile string
	inventoryWatchCancel  context.CancelFunc
	powerWatchCancel      context.CancelFunc
}

type BootstrapData struct {
	Update         update.Info            `json:"update"`
	Platform       string                 `json:"platform"`
	Mode           string                 `json:"mode"`
	BackendMode    string                 `json:"backendMode"`
	ServerProfiles clientprofile.State    `json:"serverProfiles"`
	Migration      clientmigration.Status `json:"migration"`
}

const BackendModeRemote = "remote"

type appDependencies struct {
	profilePath     string
	credentialStore credentials.Store
	httpClient      *http.Client
}

// NewApp is the V2 desktop composition root. It deliberately constructs no
// kubeconfig, Kubernetes client, or V1 session manager: all cluster operations
// are performed remotely through the configured Gateway service.
func NewApp(version string, embeddedHelperFiles fs.FS) *App {
	return newApp(version, embeddedHelperFiles, appDependencies{})
}

func newApp(version string, embeddedHelperFiles fs.FS, dependencies appDependencies) *App {
	registerEmbeddedHelpers(embeddedHelperFiles)
	if version != "" {
		helper.Version = version
	}

	profileStore, profileErr := clientprofile.Open(dependencies.profilePath)
	profilePath := ""
	if profileStore != nil {
		profilePath = profileStore.Path()
	} else if defaultPath, err := clientprofile.DefaultPath(); err == nil {
		profilePath = defaultPath
	}
	transferStatePath := ""
	if profilePath != "" {
		transferStatePath = filepath.Join(filepath.Dir(profilePath), "transfers-v2.json")
	}

	credentialStore := dependencies.credentialStore
	if credentialStore == nil {
		credentialStore = credentials.NewSystemStore()
	}
	migrationStatus := clientmigration.Status{}
	var migrationErr error
	if profilePath != "" {
		migrationStatus, migrationErr = clientmigration.PreserveLegacyState(filepath.Dir(profilePath), nil)
		if migrationErr != nil {
			migrationStatus.Error = migrationErr.Error()
		}
	}
	application := &App{
		profiles:    profileStore,
		discovery:   clientdiscovery.New(clientdiscovery.Config{ClientVersion: version, HTTPClient: dependencies.httpClient}),
		credentials: credentialStore,
		migration:   migrationStatus,
		updater:     &update.Checker{CurrentVersion: version},
		updateState: update.Info{
			CurrentVersion: version,
			URL:            "https://github.com/fengqi-dev/kube-loop/releases",
		},
	}
	if profileErr != nil {
		application.appendLog("ERROR", "V2 Server Profile store unavailable: "+profileErr.Error())
	} else if profileStore.RecoveredFromBackup() {
		application.appendLog("WARN", "V2 Server Profile store recovered from backup")
	} else {
		application.appendLog("INFO", "V2 Server Profile store loaded")
	}
	if migrationErr != nil {
		application.appendLog("ERROR", "V1 state backup failed: "+migrationErr.Error())
	} else if migrationStatus.LegacyDetected {
		application.appendLog("INFO", "V1 state preserved without importing kubeconfig, credentials, or resource intents")
	}

	application.auth = clientauth.New(clientauth.Config{HTTPClient: dependencies.httpClient, OpenBrowser: func(target string) error {
		if application.ctx == nil {
			return errors.New("application is not ready")
		}
		runtime.BrowserOpenURL(application.ctx, target)
		return nil
	}})
	remoteClient, remoteErr := clientremote.New(
		application.credentials, application.auth, clientremote.Config{HTTPClient: dependencies.httpClient},
	)
	if remoteErr != nil {
		application.appendLog("ERROR", "V2 Remote Cluster Backend unavailable: "+remoteErr.Error())
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
		application.appendLog("ERROR", "V2 file transfer manager unavailable: "+fileErr.Error())
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
		application.appendLog("ERROR", "V2 Pod exec manager unavailable: "+execErr.Error())
	} else {
		application.remoteExecs = remoteExecs
	}

	remoteSessions, sessionErr := clientremotesession.New(remoteClient, clientremotesession.Config{})
	if sessionErr != nil {
		application.appendLog("ERROR", "V2 Remote Session Manager unavailable: "+sessionErr.Error())
		return application
	}
	application.remoteSessions = remoteSessions
	remoteSSH, sshErr := clientpodssh.New(remoteClient, remoteSessions, clientpodssh.Config{})
	if sshErr != nil {
		application.appendLog("ERROR", "V2 Pod SSH Manager unavailable: "+sshErr.Error())
	} else {
		application.remoteSSH = remoteSSH
	}
	dataPlanes, dataPlaneErr := clientdataplane.NewManager(remoteSessions, clientdataplane.Config{
		ClientVersion: version, TUNStarter: NewSingboxRuntime(application.appendLog),
		OnStatus: func(event clientdataplane.StatusEvent) {
			if application.ctx != nil {
				runtime.EventsEmit(application.ctx, "dataplane:status", event)
			}
		},
	})
	if dataPlaneErr != nil {
		application.appendLog("ERROR", "V2 Data Plane Manager unavailable: "+dataPlaneErr.Error())
		return application
	}
	application.dataPlanes = dataPlanes
	remoteForwards, portForwardErr := clientportforward.New(remoteClient, dataPlanes)
	if portForwardErr != nil {
		application.appendLog("ERROR", "V2 Port Forward Manager unavailable: "+portForwardErr.Error())
	} else {
		application.remoteForwards = remoteForwards
	}
	remoteExchanges, exchangeErr := clientexchange.NewManager(remoteClient, clientexchange.Config{})
	if exchangeErr != nil {
		application.appendLog("ERROR", "V2 Exchange Manager unavailable: "+exchangeErr.Error())
	} else {
		application.remoteExchanges = remoteExchanges
	}
	remoteMirrors, mirrorErr := clientmirror.NewManager(remoteClient, clientmirror.Config{})
	if mirrorErr != nil {
		application.appendLog("ERROR", "V2 Mirror Manager unavailable: "+mirrorErr.Error())
	} else {
		application.remoteMirrors = remoteMirrors
	}
	remotePreviews, previewErr := clientpreview.NewManager(remoteClient, clientpreview.Config{})
	if previewErr != nil {
		application.appendLog("ERROR", "V2 Preview Manager unavailable: "+previewErr.Error())
	} else {
		application.remotePreviews = remotePreviews
	}

	mcpSettingsPath := ""
	if profilePath != "" {
		mcpSettingsPath = filepath.Join(filepath.Dir(profilePath), "mcp-v2.json")
	}
	mcpStore, mcpStoreErr := mcp.NewSystemConfigStore(mcpSettingsPath)
	if mcpStoreErr != nil {
		application.appendLog("ERROR", "V2 MCP settings store unavailable: "+mcpStoreErr.Error())
		return application
	}
	mcpDependencies := mcp.RemoteDependencies{
		Profiles: application.profiles, Gateway: application.remote,
		Sessions: application.remoteSessions, DataPlanes: application.dataPlanes,
		ExecClient: application.remote,
	}
	if application.remoteExecs != nil {
		mcpDependencies.ExecLifecycle = application.remoteExecs
	}
	if application.remoteFiles != nil {
		mcpDependencies.Files = application.remoteFiles
	}
	if application.remoteForwards != nil {
		mcpDependencies.Forwards = application.remoteForwards
	}
	if application.remoteExchanges != nil {
		mcpDependencies.Exchanges = application.remoteExchanges
	}
	if application.remoteMirrors != nil {
		mcpDependencies.Mirrors = application.remoteMirrors
	}
	if application.remotePreviews != nil {
		mcpDependencies.Previews = application.remotePreviews
	}
	mcpBackend, mcpBackendErr := mcp.NewRemoteBackend(mcpDependencies)
	if mcpBackendErr != nil {
		application.appendLog("ERROR", "V2 MCP Gateway backend unavailable: "+mcpBackendErr.Error())
		return application
	}
	mcpController, mcpErr := mcp.NewController(mcpBackend, mcpStore, version, application.appendLog)
	if mcpErr != nil {
		application.appendLog("ERROR", "V2 MCP controller unavailable: "+mcpErr.Error())
		return application
	}
	application.mcp = mcpController
	return application
}

func StartupHandler(a *App) func(context.Context) { return a.startup }

func ShutdownHandler(a *App) func(context.Context) { return a.shutdown }

func ShowWindow(a *App) {
	if a.ctx == nil {
		return
	}
	runtime.WindowUnminimise(a.ctx)
	runtime.WindowShow(a.ctx)
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.once.Do(func() {
		a.appendLog("INFO", "V2 application startup initialized")
		watcher, err := powerwatch.New(powerwatch.Config{OnWake: func(event powerwatch.Event) {
			if a.dataPlanes == nil {
				return
			}
			profiles := a.dataPlanes.ResumeAll()
			a.appendLog("INFO", fmt.Sprintf(
				"System wake detected after %s; refreshing %d Data Plane profile(s)", event.SleptFor, profiles,
			))
		}})
		if err != nil {
			a.appendLog("ERROR", "Power wake monitor unavailable: "+err.Error())
		} else {
			watchContext, cancel := context.WithCancel(ctx)
			a.powerWatchCancel = cancel
			go watcher.Run(watchContext)
		}
		if a.mcp != nil {
			a.mcp.StartFromStore()
		}
		go func() {
			state := a.checkForUpdates(ctx)
			runtime.EventsEmit(ctx, "update:state", state)
		}()
	})
}

func (a *App) shutdown(context.Context) {
	shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if a.powerWatchCancel != nil {
		a.powerWatchCancel()
	}
	a.stopServerInventoryWatch("")
	if a.mcp != nil {
		if err := a.mcp.Stop(); err != nil {
			log.Printf("MCP shutdown: %v", err)
		}
	}
	if a.remoteFiles != nil {
		if err := a.remoteFiles.Shutdown(); err != nil {
			log.Printf("remote file transfer shutdown: %v", err)
		}
	}
	if a.remoteExecs != nil {
		if err := a.remoteExecs.Shutdown(); err != nil {
			log.Printf("remote Pod exec shutdown: %v", err)
		}
	}
	if a.remoteSSH != nil {
		if err := a.remoteSSH.Shutdown(); err != nil {
			log.Printf("remote Pod SSH shutdown: %v", err)
		}
	}
	if a.remoteForwards != nil {
		if err := a.remoteForwards.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Port Forward shutdown: %v", err)
		}
	}
	if a.remoteExchanges != nil {
		if err := a.remoteExchanges.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Exchange shutdown: %v", err)
		}
	}
	if a.remoteMirrors != nil {
		if err := a.remoteMirrors.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Mirror shutdown: %v", err)
		}
	}
	if a.remotePreviews != nil {
		if err := a.remotePreviews.Shutdown(shutdownContext); err != nil {
			log.Printf("remote Preview shutdown: %v", err)
		}
	}
	if a.dataPlanes != nil {
		if err := a.dataPlanes.Shutdown(); err != nil {
			log.Printf("data plane shutdown: %v", err)
		}
	}
	if a.remoteSessions != nil {
		if err := a.remoteSessions.Shutdown(shutdownContext); err != nil {
			log.Printf("remote session shutdown: %v", err)
		}
	}
}

func (a *App) context() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func (a *App) appendLog(level, message string) {
	log.Printf("[%s] %s", level, message)
}

func (a *App) Bootstrap() (BootstrapData, error) {
	profiles := a.serverProfiles()
	mode := "setup"
	if profiles.ActiveProfileID != "" {
		mode = "v2"
	}
	a.updateMu.RLock()
	updateState := a.updateState
	a.updateMu.RUnlock()
	return BootstrapData{
		Update: updateState, Platform: goruntime.GOOS, Mode: mode,
		BackendMode: BackendModeRemote, ServerProfiles: profiles, Migration: a.migration,
	}, nil
}
