package runtime

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
)

func selectTUNAddress() (string, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "", fmt.Errorf("list network interfaces: %w", err)
	}
	occupied := make([]netip.Prefix, 0, len(interfaces))
	for _, item := range interfaces {
		addresses, addressErr := item.Addrs()
		if addressErr != nil {
			continue
		}
		for _, raw := range addresses {
			prefix, parseErr := netip.ParsePrefix(raw.String())
			if parseErr == nil && prefix.Addr().Is4() {
				occupied = append(occupied, prefix.Masked())
			}
		}
	}
	return selectTUNAddressFrom(occupied)
}

func selectTUNAddressFrom(occupied []netip.Prefix) (string, error) {
	for _, secondOctet := range []int{19, 18} {
		for thirdOctet := range 256 {
			for fourthOctet := 0; fourthOctet <= 252; fourthOctet += 4 {
				raw := fmt.Sprintf(
					"198.%d.%d.%d/30",
					secondOctet,
					thirdOctet,
					fourthOctet,
				)
				candidate, err := netip.ParsePrefix(raw)
				if err != nil {
					return "", err
				}
				candidate = candidate.Masked()
				if !prefixOccupied(candidate, occupied) {
					return candidate.Addr().Next().String() + "/30", nil
				}
			}
		}
	}
	return "", errors.New("no free TUN subnet is available in 198.18.0.0/15")
}

func prefixOccupied(candidate netip.Prefix, occupied []netip.Prefix) bool {
	for _, existing := range occupied {
		if !existing.Addr().Is4() {
			continue
		}
		existing = existing.Masked()
		if candidate.Contains(existing.Addr()) || existing.Contains(candidate.Addr()) {
			return true
		}
	}
	return false
}
