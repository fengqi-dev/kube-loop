// Package ui serves the dependency-free browser Management Plane shell.
package ui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/labstack/echo/v5"
)

//go:embed assets/index.html assets/app.css assets/app.js
var assets embed.FS

type Handler struct {
	assets         fs.FS
	managementPath string
}

func New(managementPaths ...string) *Handler {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		panic(err)
	}
	managementPath := controlplane.AdminAPIPathPrefix
	if len(managementPaths) > 0 && strings.HasPrefix(managementPaths[0], "/") {
		managementPath = strings.TrimSuffix(managementPaths[0], "/")
	}
	return &Handler{assets: sub, managementPath: managementPath}
}

func (handler *Handler) RegisterRoutes(group *echo.Group) {
	group.Match([]string{http.MethodGet, http.MethodHead}, "", handler.serve)
	group.Match([]string{http.MethodGet, http.MethodHead}, "/*", handler.serve)
}

func (handler *Handler) serve(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	path := "/" + strings.TrimPrefix(ctx.Param("*"), "/")
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
		return nil
	}
	content, err := fs.ReadFile(handler.assets, strings.TrimPrefix(path, "/"))
	if err != nil {
		http.NotFound(writer, request)
		return nil
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
	return nil
}
