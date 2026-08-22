package websocket

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"

	gorilla "github.com/gorilla/websocket"
)

func Accept(writer http.ResponseWriter, request *http.Request, options *AcceptOptions) (*Conn, error) {
	upgrader := gorilla.Upgrader{}
	if options != nil {
		upgrader.Subprotocols = append([]string(nil), options.Subprotocols...)
		upgrader.EnableCompression = options.CompressionMode != CompressionDisabled
		upgrader.CheckOrigin = originChecker(options)
	}
	responseHeader := writer.Header().Clone()
	connection, err := upgrader.Upgrade(writer, request, responseHeader)
	if err != nil {
		return nil, fmt.Errorf("accept WebSocket: %w", err)
	}
	return newConn(connection), nil
}

func originChecker(options *AcceptOptions) func(*http.Request) bool {
	return func(request *http.Request) bool {
		if options.InsecureSkipVerify {
			return true
		}
		origin := request.Header.Get("Origin")
		if origin == "" {
			return true
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			return false
		}
		if equalHost(parsed.Host, request.Host) {
			return true
		}
		originHost := strings.ToLower(parsed.Host)
		originURL := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
		for _, pattern := range options.OriginPatterns {
			candidate := originHost
			if strings.Contains(pattern, "://") {
				candidate = originURL
			}
			matched, matchErr := path.Match(strings.ToLower(pattern), candidate)
			if matchErr == nil && matched {
				return true
			}
		}
		return false
	}
}

func equalHost(left, right string) bool {
	return strings.EqualFold(stripDefaultPort(left), stripDefaultPort(right))
}

func stripDefaultPort(host string) string {
	name, port, err := net.SplitHostPort(host)
	if err != nil || (port != "80" && port != "443") {
		return host
	}
	return name
}
