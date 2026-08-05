package app

import (
	"context"
	"io/fs"
	"log"
	"path/filepath"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	loopmcp "github.com/fengqi-dev/kube-loop/internal/mcp"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/store"
	"github.com/fengqi-dev/kube-loop/internal/update"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

type App struct {
	ctx         context.Context
	provider    *cluster.Provider
	manager     *session.Manager
	store       *store.Store
	fileManager *filemanager.Manager
	updater     *update.Checker
	mcp         *loopmcp.Controller
	once        sync.Once
	updateMu    sync.RWMutex
	updateCheck sync.Mutex
	updateState update.Info
}

type BootstrapData struct {
	Contexts           []cluster.ContextInfo        `json:"contexts"`
	Namespaces         []string                     `json:"namespaces"`
	Session            session.State                `json:"session"`
	Update             update.Info                  `json:"update"`
	PreferredContext   string                       `json:"preferredContext,omitempty"`
	PreferredNamespace string                       `json:"preferredNamespace,omitempty"`
	PreferredMode      session.ConnectionMode       `json:"preferredMode,omitempty"`
	Platform           string                       `json:"platform"`
	KubeconfigFiles    []cluster.KubeconfigFileInfo `json:"kubeconfigFiles,omitempty"`
}

func NewApp(version string, embeddedHelperFiles fs.FS) *App {
	registerEmbeddedHelpers(embeddedHelperFiles)
	if version != "" {
		helper.Version = version
	}
	provider := cluster.NewProvider()
	provider.SetUserAgent(version)
	stateStore, err := store.Open("")
	if err != nil {
		log.Printf("open state store: %v", err)
		stateStore = nil
	}
	if stateStore != nil {
		provider.SetExtraKubeconfigFiles(stateStore.KubeconfigFiles())
	}
	options := []session.Option{
		session.WithGatewayImage(session.ResolveGatewayImage(version)),
	}
	if stateStore != nil {
		options = append(options, session.WithStore(stateStore))
	}
	manager := session.NewManager(provider, options...)
	transferStatePath := ""
	if stateStore != nil {
		transferStatePath = filepath.Join(filepath.Dir(stateStore.Path()), "transfers.json")
	}
	if err != nil {
		manager.AppendLog("ERROR", "state store unavailable: "+err.Error())
	} else {
		manager.AppendLog("INFO", "state store loaded")
	}
	return &App{
		provider:    provider,
		manager:     manager,
		store:       stateStore,
		fileManager: filemanager.NewManager(provider, transferStatePath),
		updater:     &update.Checker{CurrentVersion: version},
		updateState: update.Info{
			CurrentVersion: version,
			URL:            "https://github.com/fengqi-dev/kube-loop/releases",
		},
		mcp: loopmcp.NewController(provider, manager, stateStore, version),
	}
}

func StartupHandler(a *App) func(context.Context) {
	return a.startup
}

func ShutdownHandler(a *App) func(context.Context) {
	return a.shutdown
}

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
		a.manager.Subscribe(func(state session.State) {
			runtime.EventsEmit(ctx, "session:state", state)
		})
		a.manager.SubscribeMetrics(func(metrics *singbox.Metrics) {
			if metrics != nil {
				runtime.EventsEmit(ctx, "session:metrics", metrics)
			}
		})
		a.fileManager.Subscribe(func(task filemanager.TransferTask) {
			runtime.EventsEmit(ctx, "transfer:updated", task)
		})
		a.manager.AppendLog("INFO", "application startup initialized")
		go func() {
			state := a.checkForUpdates(ctx)
			runtime.EventsEmit(ctx, "update:state", state)
		}()
		go a.manager.RestoreStartup(ctx)
		if a.mcp != nil {
			a.mcp.StartFromStore()
		}
	})
}

func (a *App) shutdown(context.Context) {
	if a.fileManager != nil {
		a.fileManager.Shutdown()
	}
	if a.mcp != nil {
		_ = a.mcp.Stop()
	}
	if err := a.manager.Shutdown(); err != nil {
		log.Printf("application shutdown: %v", err)
	}
}
