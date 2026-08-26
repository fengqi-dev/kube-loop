//go:build !bindings

package app

import (
	"github.com/gogpu/systray"
)

// New creates and shows the desktop system tray.
func New(app *App, icon []byte) Tray {
	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open KubeLoop", func() {
		ShowWindow(app)
	})
	menu.AddSeparator()
	menu.Add("Quit KubeLoop", func() {
		tray.Remove()
		Quit(app)
	})
	tray.SetIcon(icon).
		SetTooltip("KubeLoop").
		SetMenu(menu).
		OnClick(func() { ShowWindow(app) }).
		Show()
	return tray
}
