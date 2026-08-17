package httpauth

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/labstack/echo/v5"
)

//go:embed ui/assets/index.html ui/assets/app.css ui/assets/app.js
var authUIAssets embed.FS

func (routes *Routes) authUI(ctx *echo.Context) error {
	if len(ctx.Request().URL.Query()["scope"]) > 1 {
		return ctx.NoContent(http.StatusBadRequest)
	}
	path := strings.TrimPrefix(ctx.Param("*"), "/")
	if path == "" {
		path = "index.html"
	}
	if path != "index.html" && path != "app.css" && path != "app.js" {
		return ctx.NoContent(http.StatusNotFound)
	}
	content, err := fs.ReadFile(authUIAssets, "ui/assets/"+path)
	if err != nil {
		return ctx.NoContent(http.StatusNotFound)
	}
	contentType := "text/html; charset=utf-8"
	if path == "app.css" {
		contentType = "text/css; charset=utf-8"
	}
	if path == "app.js" {
		contentType = "text/javascript; charset=utf-8"
	}
	if path == "index.html" {
		document := strings.ReplaceAll(string(content), `src="./app.js"`, `src="/oauth2/ui/app.js"`)
		document = strings.ReplaceAll(document, `href="./app.css"`, `href="/oauth2/ui/app.css"`)
		content = []byte(document)
	}
	header := ctx.Response().Header()
	header.Set("Content-Type", contentType)
	header.Set("Cache-Control", "no-store")
	header.Set("Cross-Origin-Opener-Policy", "same-origin")
	header.Set("X-Frame-Options", "DENY")
	// Chrome applies form-action to redirects after a form submission. The
	// native OAuth flow returns a 303 to the desktop client's random loopback
	// listener, so that exact address family must be allowed here as well.
	header.Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self'; form-action 'self' http://127.0.0.1:*; frame-ancestors 'none'; base-uri 'none'")
	return ctx.Blob(http.StatusOK, contentType, content)
}
