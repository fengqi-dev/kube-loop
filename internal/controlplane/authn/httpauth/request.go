package httpauth

import (
	"net/http"
	"net/url"
	"strings"

	"github.com/labstack/echo/v5"
)

func (routes *Routes) bindForm(ctx *echo.Context) (url.Values, bool) {
	if !strings.HasPrefix(
		strings.ToLower(ctx.Request().Header.Get("Content-Type")),
		"application/x-www-form-urlencoded",
	) {
		_ = routes.oauthError(
			ctx,
			http.StatusBadRequest,
			"invalid_request",
			"form-encoded request body is required",
		)
		return nil, false
	}
	if err := ctx.Request().ParseForm(); err != nil ||
		duplicateParameter(ctx.Request().PostForm) {
		_ = routes.oauthError(
			ctx,
			http.StatusBadRequest,
			"invalid_request",
			"request form is invalid",
		)
		return nil, false
	}
	return ctx.Request().PostForm, true
}

func (routes *Routes) oauthError(
	ctx *echo.Context,
	status int,
	code, description string,
) error {
	return ctx.JSON(
		status,
		errorResponse{Error: code, ErrorDescription: description},
	)
}

func duplicateParameter(values url.Values) bool {
	for _, items := range values {
		if len(items) != 1 {
			return true
		}
	}
	return false
}

func browserLoginErrorURL(form url.Values, code string) string {
	raw := form.Get("return_to")
	if !strings.HasPrefix(raw, "?") {
		return ""
	}
	query, err := url.ParseQuery(strings.TrimPrefix(raw, "?"))
	if err != nil || len(query[queryTransaction]) != 1 || len(query[queryCSRF]) != 1 ||
		query.Get(queryTransaction) == "" ||
		query.Get(queryTransaction) != form.Get(queryTransaction) ||
		query.Get(queryCSRF) == "" ||
		query.Get(queryCSRF) != form.Get(queryCSRF) {
		return ""
	}
	query.Set("error", code)
	return oauthPath + "/ui/?" + query.Encode()
}

func writeBrowserError(ctx *echo.Context) error {
	ctx.Response().
		Header().
		Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	return ctx.String(
		http.StatusBadRequest,
		"KubeLoop login failed. Return to the application and try again.\n",
	)
}
