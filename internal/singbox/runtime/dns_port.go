//go:build !windows

package runtime

func selectDNSPort() (int, error) {
	return availablePort()
}
