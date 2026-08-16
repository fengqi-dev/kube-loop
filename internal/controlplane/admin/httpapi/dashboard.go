package httpapi

import (
	"context"
	"net/http"
	"slices"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/labstack/echo/v5"
)

const overviewCountLimit = 101

type bootstrapIdentityDocument struct {
	ID          string   `json:"id"`
	DisplayName string   `json:"displayName"`
	Email       string   `json:"email,omitempty"`
	Type        string   `json:"type"`
	Groups      []string `json:"groups"`
}

type bootstrapSessionDocument struct {
	AuthenticationType string    `json:"authenticationType"`
	CreatedAt          time.Time `json:"createdAt"`
	LastSeenAt         time.Time `json:"lastSeenAt"`
	IdleExpiresAt      time.Time `json:"idleExpiresAt"`
	AbsoluteExpiresAt  time.Time `json:"absoluteExpiresAt"`
}

type bootstrapAuthorizationDocument struct {
	Administrator bool     `json:"administrator"`
	Namespaces    []string `json:"namespaces"`
}

type bootstrapDocument struct {
	Identity      bootstrapIdentityDocument      `json:"identity"`
	Session       bootstrapSessionDocument       `json:"session"`
	Authorization bootstrapAuthorizationDocument `json:"authorization"`
}

type overviewCountDocument struct {
	Count     int  `json:"count"`
	Truncated bool `json:"truncated"`
}

type overviewRelayDocument struct {
	Total        int    `json:"total"`
	Online       int    `json:"online"`
	Ready        int    `json:"ready"`
	Draining     int    `json:"draining"`
	Reservations uint64 `json:"reservations"`
}

func (api *readAPI) bootstrap(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	subject := subjectFromRequest(request)
	stored, ok := request.Context().Value(sessionContextKey).(storage.AdminSession)
	if !ok {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management authentication failed", requestID(request))
		return nil
	}
	administrator := api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{
		Resource: adminauthorization.ResourceStatus, Operation: adminauthorization.OperationRead,
	}).Allowed
	namespaces := api.authorizer.AuthorizedNamespaces(subject)
	groups := slices.Clone(subject.Groups)
	if groups == nil {
		groups = []string{}
	}
	identity, err := api.status.Identities().GetByID(request.Context(), subject.ID)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthenticated", "management identity is unavailable", requestID(request))
		return nil
	}
	api.audit(request, subject, "admin.bootstrap/read", "success")
	writeJSON(writer, http.StatusOK, bootstrapDocument{
		Identity: bootstrapIdentityDocument{ID: subject.ID, DisplayName: identity.DisplayName,
			Email: identity.PrimaryEmail, Type: identity.Type, Groups: groups},
		Session: bootstrapSessionDocument{
			AuthenticationType: stored.AuthenticationType,
			CreatedAt:          stored.CreatedAt, LastSeenAt: stored.LastSeenAt,
			IdleExpiresAt: stored.IdleExpiresAt, AbsoluteExpiresAt: stored.AbsoluteExpiresAt,
		},
		Authorization: bootstrapAuthorizationDocument{
			Administrator: administrator, Namespaces: namespaces,
		},
	})
	return nil
}

func (api *readAPI) overview(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	requestContext := request.Context()
	subject := subjectFromRequest(request)
	if err := api.status.Check(requestContext); err != nil {
		api.overviewUnavailable(writer, request, subject)
		return nil
	}
	schemaVersion, err := api.status.SchemaVersion(requestContext)
	if err != nil {
		api.overviewUnavailable(writer, request, subject)
		return nil
	}
	activeSessions, err := api.status.Sessions().List(requestContext, storage.SessionListFilter{
		State: "active", Limit: overviewCountLimit,
	})
	if err != nil {
		api.overviewUnavailable(writer, request, subject)
		return nil
	}
	activeTaskCount, activeTasksTruncated, err := api.activeTaskCount(requestContext)
	if err != nil {
		api.overviewUnavailable(writer, request, subject)
		return nil
	}
	relays := api.overviewRelays()
	recentAudit := []auditDocument{}
	if api.authorizer.Authorize(requestContext, subject, adminauthorization.Request{
		Resource: adminauthorization.ResourceAudit, Operation: adminauthorization.OperationList,
	}).Allowed {
		events, listErr := api.status.Audit().List(requestContext, storage.AuditFilter{Limit: 5})
		if listErr != nil {
			api.overviewUnavailable(writer, request, subject)
			return nil
		}
		recentAudit = make([]auditDocument, 0, len(events))
		for _, event := range events {
			recentAudit = append(recentAudit, auditDocument{
				ID: event.ID, IdentityID: event.IdentityID, Action: event.Action,
				ResourceType: event.ResourceType, ResourceID: event.ResourceID,
				Outcome: event.Outcome, RequestID: event.RequestID, CreatedAt: event.CreatedAt,
			})
		}
	}
	api.audit(request, subject, "admin.overview/read", "success")
	writeJSON(writer, http.StatusOK, map[string]any{
		"generatedAt": time.Now().UTC(),
		"system": map[string]any{
			"controlPlane": map[string]string{
				"version": api.build.Version, "commit": api.build.Commit,
				"protocolMin": api.build.ProtocolMin, "protocolMax": api.build.ProtocolMax,
			},
			"storage": map[string]any{
				"status": "ready", "backend": api.status.Backend(), "schemaVersion": schemaVersion,
			},
		},
		"security": map[string]any{"authenticationType": subject.Authentication},
		"runtime": map[string]any{
			"activeSessions": overviewCountDocument{
				Count: min(len(activeSessions), overviewCountLimit-1), Truncated: len(activeSessions) == overviewCountLimit,
			},
			"activeTasks": overviewCountDocument{Count: activeTaskCount, Truncated: activeTasksTruncated},
			"relays":      relays,
		},
		"recentAudit": recentAudit,
	})
	return nil
}

func (api *readAPI) activeTaskCount(ctx context.Context) (int, bool, error) {
	count := 0
	truncated := false
	for _, state := range []remotetask.State{
		remotetask.Pending, remotetask.Starting, remotetask.Running, remotetask.Recovering, remotetask.Stopping,
	} {
		tasks, err := api.status.Tasks().List(ctx, storage.TaskListFilter{State: state, Limit: overviewCountLimit})
		if err != nil {
			return 0, false, err
		}
		if len(tasks) == overviewCountLimit {
			truncated = true
			count += overviewCountLimit - 1
		} else {
			count += len(tasks)
		}
	}
	return count, truncated, nil
}

func (api *readAPI) overviewRelays() overviewRelayDocument {
	document := overviewRelayDocument{}
	if api.relays == nil {
		return document
	}
	for _, status := range api.relays.Snapshot() {
		document.Total++
		if status.Online {
			document.Online++
		}
		if status.State == relaycontrol.StateReady {
			document.Ready++
		}
		if status.State == relaycontrol.StateDraining || status.DesiredState == relaycontrol.StateDraining {
			document.Draining++
		}
		document.Reservations += uint64(status.Reservations)
	}
	return document
}

func (api *readAPI) overviewUnavailable(
	writer http.ResponseWriter,
	request *http.Request,
	subject adminauthorization.Subject,
) {
	api.audit(request, subject, "admin.overview/read", "failure")
	writeError(writer, http.StatusServiceUnavailable, "unavailable", "management overview is unavailable", requestID(request))
}
