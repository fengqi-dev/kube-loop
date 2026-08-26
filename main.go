package main

import (
	"embed"
	"log"
	"log/slog"
	"os"
	goruntime "runtime"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	macoptions "github.com/wailsapp/wails/v2/pkg/options/mac"

	desktopapp "github.com/fengqi-dev/kube-loop/internal/app"
	internalLogging "github.com/fengqi-dev/kube-loop/internal/logging"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

// The README keeps the pattern valid for ordinary source builds. Release and
// IDE builds generate the platform helper in this directory before compiling
// the desktop application.
//
//go:embed build/embedded/*
var embeddedHelperFiles embed.FS

var version = "dev"

func main() {
	jsonLogger := slog.New(internalLogging.WithContext(slog.NewJSONHandler(os.Stderr, nil)))
	slog.SetDefault(jsonLogger)
	log.SetOutput(slog.NewLogLogger(jsonLogger.Handler(), slog.LevelInfo).Writer())
	// The tray and Wails windows must be created on the same native UI thread.
	goruntime.LockOSThread()

	app := desktopapp.NewApp(version, embeddedHelperFiles)
	tray := desktopapp.New(app, trayIcon)
	err := wails.Run(&options.App{
		Title:             "KubeLoop",
		Width:             900,
		Height:            580,
		MinWidth:          900,
		MinHeight:         580,
		MaxWidth:          900,
		MaxHeight:         580,
		DisableResize:     true,
		Frameless:         true,
		HideWindowOnClose: true,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.fengqi.kube-loop",
			OnSecondInstanceLaunch: func(instance options.SecondInstanceData) {
				for _, argument := range instance.Args {
					if !strings.HasPrefix(strings.ToLower(argument), "kubeloop://") {
						continue
					}
					if deliverAuthCallback(app, argument) {
						break
					}
				}
				desktopapp.ShowWindow(app)
			},
		},
		Mac: &macoptions.Options{
			OnUrlOpen: func(rawURL string) {
				deliverAuthCallback(app, rawURL)
			},
		},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        desktopapp.StartupHandler(app),
		OnShutdown:       desktopapp.ShutdownHandler(app),
		Bind:             []any{app},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
	})
	goruntime.UnlockOSThread()
	if err != nil {
		desktopapp.Remove(tray)
		log.Fatal(err)
	}
}

type authCallbackHandler interface {
	HandleAuthCallbackURL(string) error
}

func deliverAuthCallback(handler authCallbackHandler, rawURL string) bool {
	if handler == nil {
		log.Print("OAuth callback rejected: callback handler is unavailable")
		return false
	}
	if err := handler.HandleAuthCallbackURL(rawURL); err != nil {
		log.Printf("OAuth callback rejected: %v", err)
		return false
	}
	return true
}
