//go:build windows

package install

// Windows does not allow replacing a running executable. Stop the existing
// service before atomically publishing an upgraded helper.
func prepareBinaryInstall() error {
	return stopServiceForUpgrade()
}
