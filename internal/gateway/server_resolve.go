package gateway

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/correlation"
)

func resolvePrivate(ctx context.Context, host string, port uint16) (string, error) {
	if strings.EqualFold(host, "localhost") {
		return "", errors.New("loopback targets are not allowed")
	}
	addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", fmt.Errorf("resolve target: %w", err)
	}
	for _, address := range addresses {
		ip, ok := netip.AddrFromSlice(address.AsSlice())
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if isClusterAddress(ip) {
			return net.JoinHostPort(ip.String(), strconv.FormatUint(uint64(port), 10)), nil
		}
	}
	return "", fmt.Errorf("target %q does not resolve to a private cluster address", host)
}

func isClusterAddress(ip netip.Addr) bool {
	return ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

func (s *Server) log(ctx context.Context, requestID, message string, attributes ...any) {
	if s.Logger != nil {
		arguments := make([]any, 0, len(attributes)+6)
		arguments = append(
			arguments,
			"operation", "gateway.tunnel.stream",
			"outcome", "failure",
			"correlation_id", correlation.ID(ctx),
			"request_id", requestID,
		)
		arguments = append(arguments, attributes...)
		s.Logger.WarnContext(ctx, message, arguments...)
	}
}
