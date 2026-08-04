package main

import (
	"embed"
	"log"

	desktopapp "github.com/fengqi-dev/kube-loop/internal/app"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// The README keeps the pattern valid for ordinary source builds. Release and
// IDE builds generate the platform helper in this directory before compiling
// the desktop application.
//
//go:embed build/embedded/*
var embeddedHelperFiles embed.FS

var version = "dev"

func main() {
	app := desktopapp.NewApp(version, embeddedHelperFiles)
	if err := wails.Run(&options.App{
		Title:         "KubeLoop",
		Width:         1080,
		Height:        720,
		MinWidth:      1080,
		MinHeight:     720,
		MaxWidth:      1080,
		MaxHeight:     720,
		DisableResize: true,
		Frameless:     true,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.fengqi.kube-loop",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				desktopapp.ShowWindow(app)
			},
		},
		AssetServer:      &assetserver.Options{Assets: assets},
		OnStartup:        desktopapp.StartupHandler(app),
		OnShutdown:       desktopapp.ShutdownHandler(app),
		Bind:             []any{app},
		BackgroundColour: &options.RGBA{R: 15, G: 23, B: 42, A: 1},
	}); err != nil {
		log.Fatal(err)
	}
}
