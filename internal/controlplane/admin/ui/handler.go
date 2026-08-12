// Package ui serves the dependency-free browser Management Plane shell.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/index.html assets/app.css assets/app.js
var assets embed.FS

type Handler struct {
	assets         fs.FS
	managementPath string
}

func New(managementPaths ...string) http.Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	managementPath := "/kubeloop/api/admin"
	if len(managementPaths) > 0 && strings.HasPrefix(managementPaths[0], "/") {
		managementPath = strings.TrimSuffix(managementPaths[0], "/")
	}
	return &Handler{assets: sub, managementPath: managementPath}
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		writer.Header().Set("Allow", "GET, HEAD")
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	path := strings.TrimPrefix(request.URL.Path, "/ui")
	if path == "" || path == "/" || path == "/callback" {
		path = "/index.html"
	}
	var contentType string
	switch path {
	case "/index.html":
		contentType = "text/html; charset=utf-8"
	case "/app.css":
		contentType = "text/css; charset=utf-8"
	case "/app.js":
		contentType = "text/javascript; charset=utf-8"
	default:
		http.NotFound(writer, request)
		return
	}
	content, err := fs.ReadFile(handler.assets, strings.TrimPrefix(path, "/"))
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	if path == "/index.html" {
		content = []byte(strings.ReplaceAll(string(content), "{{MANAGEMENT_PATH}}", handler.managementPath))
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
	writer.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; connect-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'")
	writer.Header().Set("X-Frame-Options", "DENY")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if request.Method == http.MethodGet {
		_, _ = writer.Write(content)
	}
}
