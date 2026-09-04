package utils

import (
	"net"
	"strconv"
	"strings"
)

// IsAddressAlreadyInUse reports whether err is a bind failure caused by another
// listener holding the address. The check is textual because the wording differs
// between the POSIX and Windows socket layers and the error often arrives
// wrapped by a runtime that does not preserve the syscall value.
func IsAddressAlreadyInUse(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}

// CloneUDPAddress copies address so callers can retain a peer address beyond the
// read that produced it, which reuses its backing IP slice.
func CloneUDPAddress(address *net.UDPAddr) *net.UDPAddr {
	return &net.UDPAddr{IP: append(net.IP(nil), address.IP...), Port: address.Port, Zone: address.Zone}
}

// ValidLocalHost reports whether host is usable as the local side of a traffic
// target: either a concrete IP that is neither unspecified nor multicast, or a
// syntactically valid DNS name.
func ValidLocalHost(host string) bool {
	if address := net.ParseIP(host); address != nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	if len(host) > 253 || strings.ContainsAny(host, " /\\\t\r\n") {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

// TargetKey builds the canonical protocol/port identity used to pair a local
// traffic target with the Service port it serves.
func TargetKey(protocol string, port int32) string {
	return strings.ToLower(protocol) + "/" + strconv.Itoa(int(port))
}
