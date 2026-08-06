//go:build !windows

package networkdiag

import (
	"fmt"
	"net"
	"net/netip"
)

func readHostRoutes() ([]hostRoute, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("list network interfaces: %w", err)
	}
	var routes []hostRoute
	for _, item := range interfaces {
		if item.Flags&net.FlagUp == 0 || item.Flags&net.FlagLoopback != 0 {
			continue
		}
		addresses, addressErr := item.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr == nil {
				routes = append(routes, hostRoute{
					Prefix: prefix.Masked(), Interface: item.Name,
				})
			}
		}
	}
	return routes, nil
}
