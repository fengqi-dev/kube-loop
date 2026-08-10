package controller

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

type APIRouter struct {
	mux      *chi.Mux
	routes   map[string]struct{}
	fallback APIHandler
}

type apiDispatchState struct {
	principal Principal
	apiError  *APIError
}

type apiDispatchContextKey struct{}

func NewAPIRouter() *APIRouter {
	router := &APIRouter{
		mux:    chi.NewRouter(),
		routes: make(map[string]struct{}),
	}
	router.mux.NotFound(router.serveFallback)
	router.mux.MethodNotAllowed(router.serveFallback)
	return router
}

func (router *APIRouter) Handle(method, pattern string, handler APIHandler) error {
	if router == nil {
		return errors.New("API router is nil")
	}
	method = strings.ToUpper(strings.TrimSpace(method))
	pattern = strings.TrimSpace(pattern)
	if method == "" || !strings.HasPrefix(pattern, APIPathPrefix+"/") || strings.HasSuffix(pattern, "/") || handler == nil {
		return errors.New("API route requires an HTTP method, v2 path pattern, and handler")
	}
	key := method + " " + pattern
	if _, exists := router.routes[key]; exists {
		return errors.New("API route is already registered")
	}
	router.routes[key] = struct{}{}
	router.mux.Method(method, pattern, router.adapt(handler))
	return nil
}

func (router *APIRouter) HandlePrefix(prefix string, handler APIHandler) error {
	if router == nil {
		return errors.New("API router is nil")
	}
	prefix = strings.TrimRight(strings.TrimSpace(prefix), "/")
	if !strings.HasPrefix(prefix, APIPathPrefix+"/") || handler == nil {
		return errors.New("API route requires a v2 path prefix and handler")
	}
	if strings.ContainsAny(prefix, "{}*") {
		return errors.New("API route prefix must be literal")
	}
	if _, exists := router.routes[prefix]; exists {
		return errors.New("API route prefix is already registered")
	}
	router.routes[prefix] = struct{}{}
	router.mux.Mount(prefix, router.adapt(handler))
	return nil
}

func (router *APIRouter) adapt(handler APIHandler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		state := apiDispatchStateFromContext(request.Context())
		if state == nil {
			return
		}
		state.apiError = handler.ServeAPI(writer, request, state.principal)
	})
}

func (router *APIRouter) SetFallback(handler APIHandler) {
	if router != nil {
		router.fallback = handler
	}
}

func (router *APIRouter) ServeAPI(writer http.ResponseWriter, request *http.Request, principal Principal) *APIError {
	if router == nil || router.mux == nil {
		return &APIError{Code: CodeNotFound, Message: "resource not found"}
	}
	if apiError := RequireAuthorizedRequest(request.Context()); apiError != nil {
		return apiError
	}
	state := &apiDispatchState{principal: principal}
	requestContext := context.WithValue(request.Context(), apiDispatchContextKey{}, state)
	// APIRouter receives requests after the top-level /api/v2 chi mount. Start a
	// fresh route context so its full-path registrations are not matched against
	// the parent mount's already-trimmed RoutePath.
	requestContext = context.WithValue(requestContext, chi.RouteCtxKey, chi.NewRouteContext())
	request = request.WithContext(requestContext)
	router.mux.ServeHTTP(writer, request)
	return state.apiError
}

func (router *APIRouter) serveFallback(writer http.ResponseWriter, request *http.Request) {
	state := apiDispatchStateFromContext(request.Context())
	if state == nil {
		http.NotFound(writer, request)
		return
	}
	if router.fallback != nil {
		state.apiError = router.fallback.ServeAPI(writer, request, state.principal)
		return
	}
	state.apiError = &APIError{Code: CodeNotFound, Message: "resource not found"}
}

func apiDispatchStateFromContext(requestContext context.Context) *apiDispatchState {
	state, _ := requestContext.Value(apiDispatchContextKey{}).(*apiDispatchState)
	return state
}
