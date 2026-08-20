//go:build bindings

package desktoptray

import desktopapp "github.com/fengqi-dev/kube-loop/internal/app"

// New disables the native tray in the temporary Wails bindings executable.
func New(_ *desktopapp.App, _ []byte) Tray {
	return nil
}
