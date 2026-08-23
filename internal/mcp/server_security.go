package mcp

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func localhostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		host = strings.Trim(host, "[]")
		if host != "127.0.0.1" && !strings.EqualFold(host, "localhost") {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !isLocalOrigin(origin) {
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func isLocalOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "null" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || (parsed.Scheme != transportHTTP && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return false
	}
	host := parsed.Hostname()
	return strings.EqualFold(host, "localhost") || host == "127.0.0.1" || host == "::1"
}
