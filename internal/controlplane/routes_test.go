package controlplane

import (
	"encoding/json"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
)

func testEndpoint(function any) RouteRegistrar {
	var endpoint EndpointFunc
	switch function := function.(type) {
	case EndpointFunc:
		endpoint = function
	case func(http.ResponseWriter, *http.Request, controlplaneapi.Principal) *controlplaneapi.Error:
		endpoint = func(ctx *echo.Context, principal controlplaneapi.Principal) *controlplaneapi.Error {
			return function(ctx.Response(), ctx.Request(), principal)
		}
	default:
		panic("unsupported test endpoint")
	}
	return RouteRegistrarFunc(func(group *echo.Group) {
		handler := Endpoint(endpoint)
		group.Any("", handler)
		group.Any("/*", handler)
	})
}

func writeTestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
