//go:build !bindings

package desktoptray

import (
	"github.com/gogpu/systray"

	desktopapp "github.com/fengqi-dev/kube-loop/internal/app"
)

// New creates and shows the desktop system tray.
func New(app *desktopapp.App, icon []byte) Tray {
	tray := systray.New()
	menu := systray.NewMenu()
	menu.Add("Open KubeLoop", func() {
		desktopapp.ShowWindow(app)
	})
	menu.AddSeparator()
	menu.Add("Quit KubeLoop", func() {
		tray.Remove()
		desktopapp.Quit(app)
	})
	tray.SetIcon(icon).
		SetTooltip("KubeLoop").
		SetMenu(menu).
		OnClick(func() { desktopapp.ShowWindow(app) }).
		Show()
	return tray
}
