package app

// Tray is the system tray lifecycle used by the desktop entry point.
type Tray interface {
	Remove()
}

// Remove releases a tray when the current build provides one.
func Remove(tray Tray) {
	if tray != nil {
		tray.Remove()
	}
}
