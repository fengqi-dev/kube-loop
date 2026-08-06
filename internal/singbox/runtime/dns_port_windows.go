//go:build windows

package runtime

// NRPT NameServers do not carry a port; Windows DNS clients expect UDP/TCP 53.
func selectDNSPort() (int, error) {
	return 53, nil
}
