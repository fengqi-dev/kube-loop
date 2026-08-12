package relayregistry

import (
	"errors"
	"net/url"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

// EndpointHostPolicy restricts advertised WSS endpoints to exact hosts or
// explicitly configured DNS suffixes. A suffix begins with a dot and never
// matches the bare parent domain.
func EndpointHostPolicy(allowed ...string) (EndpointPolicy, error) {
	normalized := make([]string, 0, len(allowed))
	for _, candidate := range allowed {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" || strings.ContainsAny(candidate, "/:@[]") || strings.HasSuffix(candidate, ".") ||
			candidate == "." || strings.Contains(candidate, "..") {
			return nil, errors.New("Relay endpoint host policy is invalid")
		}
		normalized = append(normalized, candidate)
	}
	if len(normalized) == 0 {
		return nil, errors.New("at least one Relay endpoint host is required")
	}
	return func(_ relaycontrol.PeerIdentity, endpoint string) error {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.New("Relay endpoint is invalid")
		}
		host := strings.ToLower(parsed.Hostname())
		for _, allowedHost := range normalized {
			if strings.HasPrefix(allowedHost, ".") {
				if strings.HasSuffix(host, allowedHost) && len(host) > len(allowedHost) {
					return nil
				}
				continue
			}
			if host == allowedHost {
				return nil
			}
		}
		return errors.New("Relay endpoint host is not allowed")
	}, nil
}
