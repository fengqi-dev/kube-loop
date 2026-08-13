package httpauth

import (
	_ "embed"
	"html/template"
	"net/http"

	"github.com/labstack/echo/v5"
)

//go:embed assets/local-login.html
var localLoginHTML string

//go:embed assets/local-login.css
var localLoginCSS []byte

var localLoginTemplate = template.Must(template.New("local-login").Parse(localLoginHTML))

type localLoginPage struct {
	Transaction string
	Action      string
}

func (routes *Routes) localLoginStyles(ctx *echo.Context) error {
	return ctx.Blob(http.StatusOK, "text/css; charset=utf-8", localLoginCSS)
}
