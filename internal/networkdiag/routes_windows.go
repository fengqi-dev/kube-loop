//go:build windows

package networkdiag

import (
	"fmt"
	"net"
	"net/netip"
	"unsafe"

	"golang.org/x/sys/windows"
)

func readHostRoutes() ([]hostRoute, error) {
	var table *windows.MibIpForwardTable2
	if err := windows.GetIpForwardTable2(windows.AF_UNSPEC, &table); err != nil {
		return nil, fmt.Errorf("read Windows route table: %w", err)
	}
	if table == nil {
		return nil, nil
	}
	defer windows.FreeMibTable(unsafe.Pointer(table))

	result := make([]hostRoute, 0, table.NumEntries)
	for _, row := range table.Rows() {
		prefix, ok := windowsRoutePrefix(row.DestinationPrefix)
		if !ok || prefix.Bits() == 0 {
			continue
		}
		name := ""
		if item, err := net.InterfaceByIndex(int(row.InterfaceIndex)); err == nil {
			name = item.Name
		}
		result = append(result, hostRoute{
			Prefix: prefix.Masked(), Interface: name, Metric: row.Metric,
		})
	}
	return result, nil
}

func windowsRoutePrefix(raw windows.IpAddressPrefix) (netip.Prefix, bool) {
	switch raw.Prefix.Family {
	case windows.AF_INET:
		value := (*windows.RawSockaddrInet4)(unsafe.Pointer(&raw.Prefix))
		address := netip.AddrFrom4(value.Addr)
		return netip.PrefixFrom(address, int(raw.PrefixLength)), true
	case windows.AF_INET6:
		value := (*windows.RawSockaddrInet6)(unsafe.Pointer(&raw.Prefix))
		address := netip.AddrFrom16(value.Addr)
		return netip.PrefixFrom(address, int(raw.PrefixLength)), true
	default:
		return netip.Prefix{}, false
	}
}
