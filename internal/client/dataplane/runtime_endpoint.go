package dataplane

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func transportURL(serverProfile profile.Profile, assignedEndpoint string) (string, error) {
	assignedEndpoint = strings.TrimSpace(assignedEndpoint)
	if assignedEndpoint == "" {
		return URL(serverProfile)
	}
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return "", err
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("server Profile URL is invalid")
	}
	endpoint, err := url.Parse(assignedEndpoint)
	if err != nil || (endpoint.Scheme != "ws" && endpoint.Scheme != dataplaneWSSScheme) || endpoint.Host == "" {
		return "", errors.New("relay assignment endpoint is invalid")
	}
	if base.Scheme == "http" {
		endpoint.Scheme = "ws"
	} else {
		endpoint.Scheme = dataplaneWSSScheme
	}
	return endpoint.String(), nil
}

func newAssignmentTokenSource(
	source func(context.Context) (remote.RelayTicket, error),
	initial remote.RelayTicket,
) func(context.Context) (string, error) {
	var mutex sync.Mutex
	first := true
	return func(ctx context.Context) (string, error) {
		mutex.Lock()
		defer mutex.Unlock()
		ticket := initial
		if first {
			first = false
		} else {
			var err error
			ticket, err = source(ctx)
			if err != nil {
				return "", err
			}
			if ticket.Endpoint != initial.Endpoint || ticket.RelayID != initial.RelayID ||
				ticket.DeviceID != initial.DeviceID {
				return "", errors.New("relay assignment changed while opening a WebSocket pool")
			}
		}
		if ticket.Ticket == "" || strings.TrimSpace(ticket.Ticket) != ticket.Ticket {
			return "", errors.New("relayTicket source returned an invalid ticket")
		}
		return ticket.Ticket, nil
	}
}

func URL(serverProfile profile.Profile) (string, error) {
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return "", err
	}
	tunnelPath := strings.TrimSpace(serverProfile.TunnelPath)
	if tunnelPath == "" {
		tunnelPath = defaultTunnelPath
	}
	parsedPath, err := url.ParseRequestURI(tunnelPath)
	if err != nil || !strings.HasPrefix(tunnelPath, "/") || parsedPath.IsAbs() || parsedPath.Host != "" ||
		parsedPath.RawQuery != "" || parsedPath.Fragment != "" || parsedPath.EscapedPath() != tunnelPath ||
		strings.Contains(
			tunnelPath,
			"//",
		) || strings.Contains(tunnelPath, "/./") || strings.Contains(tunnelPath, "/../") ||
		strings.HasSuffix(tunnelPath, "/.") || strings.HasSuffix(tunnelPath, "/..") {
		return "", errors.New("server Profile tunnel path is invalid")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return "", errors.New("server Profile URL is invalid")
	}
	switch endpoint.Scheme {
	case "https":
		endpoint.Scheme = dataplaneWSSScheme
	case "http":
		endpoint.Scheme = "ws"
	default:
		return "", errors.New("server Profile URL must use HTTP or HTTPS")
	}
	endpoint.Path = parsedPath.Path
	endpoint.RawPath = parsedPath.RawPath
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return endpoint.String(), nil
}
