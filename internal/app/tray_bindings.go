//go:build bindings

package app

// New disables the native tray in the temporary Wails bindings executable.
func New(_ *App, _ []byte) Tray {
	return nil
}
