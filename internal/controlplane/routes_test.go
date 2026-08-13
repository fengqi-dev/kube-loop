package controlplane

import (
	"encoding/json"
	"net/http"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/labstack/echo/v5"
)

func testEndpoint(function any) RouteRegistrar {
	return testRouteRegistrar(function, false)
}

func sessionTestEndpoint(function any) RouteRegistrar {
	return testRouteRegistrar(function, true)
}

func testRouteRegistrar(function any, session bool) RouteRegistrar {
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
	register := func(group *echo.Group) {
		handler := Endpoint(endpoint)
		group.Any("", handler)
		group.Any("/*", handler)
	}
	if session {
		return testSessionRouteRegistrar{registerSessionRoutes: register}
	}
	return RouteRegistrarFunc(register)
}

type testSessionRouteRegistrar struct {
	registerSessionRoutes func(*echo.Group)
}

func (testSessionRouteRegistrar) RegisterRoutes(*echo.Group) {}

func (registrar testSessionRouteRegistrar) RegisterSessionRoutes(group *echo.Group) {
	registrar.registerSessionRoutes(group)
}

func writeTestJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
