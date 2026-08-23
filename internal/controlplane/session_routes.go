package controlplane

import (
	"net/http"

	"github.com/labstack/echo/v5"
)

// RegisterSessionRoutes registers Session lifecycle and task resources under
// /api/sessions, alongside the general /api resource API.
func (routes APIRoutes) RegisterSessionRoutes(group *echo.Group) {
	registerRoute(group, http.MethodPost, "/sessions/:sessionID/tickets", routes.Tickets.Issue)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/port-forwards",
		routes.PortForwards.Create,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/port-forwards",
		routes.PortForwards.List,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/port-forwards/:taskID",
		routes.PortForwards.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exchanges", routes.Exchanges.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/exchanges/:taskID",
		routes.Exchanges.Get,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/exchanges/:taskID",
		routes.Exchanges.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/mirrors", routes.Mirrors.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID/mirrors/:taskID", routes.Mirrors.Get)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/mirrors/:taskID",
		routes.Mirrors.Stop,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/previews", routes.Previews.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/previews/:taskID",
		routes.Previews.Get,
	)
	registerRoute(
		group,
		http.MethodDelete,
		"/sessions/:sessionID/previews/:taskID",
		routes.Previews.Stop,
	)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/list",
		routes.FileOperations.List,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/create",
		routes.FileOperations.Create,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/rename",
		routes.FileOperations.Rename,
	)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/pod-files/delete",
		routes.FileOperations.Delete,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/pod-files/operations/:taskID",
		routes.FileOperations.Operation,
	)

	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/file-transfers",
		routes.FileTransfers.Create,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/file-transfers/:taskID",
		routes.FileTransfers.Get,
	)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/file-transfers/:taskID/stream",
		routes.FileTransfers.Stream,
	)

	registerRoute(group, http.MethodPost, "/sessions/:sessionID/exec", routes.Exec.Create)
	registerRoute(
		group,
		http.MethodGet,
		"/sessions/:sessionID/exec/:taskID/stream",
		routes.Exec.Stream,
	)

	registerRoute(group, http.MethodPost, "/sessions", routes.Sessions.Create)
	registerRoute(group, http.MethodGet, "/sessions/:sessionID", routes.Sessions.Get)
	registerRoute(
		group,
		http.MethodPost,
		"/sessions/:sessionID/heartbeat",
		routes.Sessions.Heartbeat,
	)
	registerRoute(group, http.MethodDelete, "/sessions/:sessionID", routes.Sessions.Disconnect)
}
