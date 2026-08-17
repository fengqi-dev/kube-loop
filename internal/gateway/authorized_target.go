package gateway

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"slices"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type IPResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}

func (s *Server) resolveAuthorized(
	ctx context.Context,
	host string,
	port uint16,
	spec networkspec.Spec,
) (string, error) {
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if err := networkspec.AuthorizeAddress(spec, address, port); err != nil {
			return "", err
		}
		return net.JoinHostPort(address.String(), strconv.Itoa(int(port))), nil
	}
	host, err := networkspec.AuthorizeDomain(spec, host)
	if err != nil {
		return "", err
	}
	resolver := s.Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addresses, err := resolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return "", errors.New("resolve authorized cluster target")
	}
	allowed := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		address = address.Unmap()
		if networkspec.AuthorizeAddress(spec, address, port) == nil {
			allowed = append(allowed, address)
		}
	}
	if len(allowed) == 0 {
		return "", errors.New("resolved target is not allowed by NetworkSpec")
	}
	slices.SortFunc(allowed, func(left, right netip.Addr) int { return left.Compare(right) })
	return net.JoinHostPort(allowed[0].String(), strconv.Itoa(int(port))), nil
}
