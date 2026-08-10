package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controller/admin/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/remotetask"
	"github.com/google/uuid"
)

const (
	defaultReadPageSize = 50
	maximumReadPageSize = 100
	maximumCursorBytes  = 512
)

var listNamespacePattern = regexp.MustCompile(`^[a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?$`)

type listParameters struct {
	limit  int
	cursor *storage.PageCursor
	query  url.Values
}

type cursorDocument struct {
	Version   int       `json:"v"`
	Kind      string    `json:"kind"`
	CreatedAt time.Time `json:"createdAt"`
	ID        string    `json:"id"`
}

type principalDocument struct {
	ID          string    `json:"id"`
	Provider    string    `json:"provider"`
	DisplayName string    `json:"displayName,omitempty"`
	Email       string    `json:"email,omitempty"`
	Groups      []string  `json:"groups"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type sessionDocument struct {
	ID                string    `json:"id"`
	PrincipalID       string    `json:"principalId"`
	DeviceID          string    `json:"deviceId"`
	ClusterID         string    `json:"clusterId"`
	Namespace         string    `json:"namespace"`
	State             string    `json:"state"`
	Generation        uint64    `json:"generation"`
	NetworkSpecSHA256 string    `json:"networkSpecSha256,omitempty"`
	CreatedAt         time.Time `json:"createdAt"`
	UpdatedAt         time.Time `json:"updatedAt"`
	LastHeartbeatAt   time.Time `json:"lastHeartbeatAt"`
	ExpiresAt         time.Time `json:"expiresAt"`
}

type taskDocument struct {
	ID          string     `json:"id"`
	PrincipalID string     `json:"principalId"`
	SessionID   string     `json:"sessionId"`
	Type        string     `json:"type"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
}

type auditDocument struct {
	ID           string    `json:"id"`
	PrincipalID  string    `json:"principalId,omitempty"`
	Action       string    `json:"action"`
	ResourceType string    `json:"resourceType,omitempty"`
	ResourceID   string    `json:"resourceId,omitempty"`
	Outcome      string    `json:"outcome"`
	RequestID    string    `json:"requestId"`
	CreatedAt    time.Time `json:"createdAt"`
}

type relayDocument struct {
	RelayID                     string                `json:"relayId"`
	State                       relaycontrol.State    `json:"state"`
	DesiredState                relaycontrol.State    `json:"desiredState"`
	Capacity                    relaycontrol.Capacity `json:"capacity"`
	AppliedKeyGeneration        uint64                `json:"appliedKeyGeneration"`
	AppliedRevocationGeneration uint64                `json:"appliedRevocationGeneration"`
	LeaseExpiresAt              time.Time             `json:"leaseExpiresAt"`
	LastHeartbeatAt             time.Time             `json:"lastHeartbeatAt"`
	Reservations                uint32                `json:"reservations"`
	Online                      bool                  `json:"online"`
}

type relayCursorDocument struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	RelayID string `json:"relayId"`
}

func (api *readAPI) listPrincipals(writer http.ResponseWriter, request *http.Request) {
	parameters, err := parseListParameters(request, "principals", "provider")
	provider := strings.TrimSpace(parameters.query.Get("provider"))
	if err != nil || len(provider) > 128 || strings.ContainsAny(provider, "\x00\r\n") {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	principals, err := api.status.Principals().List(request.Context(), storage.PrincipalListFilter{
		Provider: provider, Cursor: parameters.cursor, Limit: parameters.limit + 1,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.principal/list", "failure")
		writeListError(writer, request, http.StatusServiceUnavailable, "unavailable", "management principal list is unavailable")
		return
	}
	hasMore := len(principals) > parameters.limit
	if hasMore {
		principals = principals[:parameters.limit]
	}
	items := make([]principalDocument, 0, len(principals))
	for _, principal := range principals {
		items = append(items, principalDocument{
			ID: principal.ID, Provider: principal.Provider, DisplayName: principal.DisplayName, Email: principal.Email,
			Groups: slices.Clone(principal.Groups), CreatedAt: principal.CreatedAt, UpdatedAt: principal.UpdatedAt,
		})
	}
	api.audit(request, subjectFromRequest(request), "admin.principal/list", "success")
	writeJSON(writer, http.StatusOK, listResponse("principals", items, principals, hasMore))
}

func (api *readAPI) listSessions(writer http.ResponseWriter, request *http.Request) {
	parameters, err := parseListParameters(request, "sessions", "principalId", "namespace", "state")
	principalID := strings.TrimSpace(parameters.query.Get("principalId"))
	namespace := strings.TrimSpace(parameters.query.Get("namespace"))
	state := strings.TrimSpace(parameters.query.Get("state"))
	if err != nil || !validOptionalUUID(principalID) || !validOptionalNamespace(namespace) ||
		len(state) > 64 || strings.ContainsAny(state, "\x00\r\n") {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	if !api.authorizeScopedList(request, adminauthorization.ResourceSession, namespace) {
		writeListError(writer, request, http.StatusForbidden, "forbidden", "management operation is not permitted")
		return
	}
	sessions, err := api.status.Sessions().List(request.Context(), storage.SessionListFilter{
		PrincipalID: principalID, Namespace: namespace, State: state,
		Cursor: parameters.cursor, Limit: parameters.limit + 1,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.session/list", "failure")
		writeListError(writer, request, http.StatusServiceUnavailable, "unavailable", "management session list is unavailable")
		return
	}
	hasMore := len(sessions) > parameters.limit
	if hasMore {
		sessions = sessions[:parameters.limit]
	}
	items := make([]sessionDocument, 0, len(sessions))
	for _, session := range sessions {
		items = append(items, sessionDocument{
			ID: session.ID, PrincipalID: session.PrincipalID, DeviceID: session.DeviceID,
			ClusterID: session.ClusterID, Namespace: session.Namespace, State: session.State,
			Generation: session.Generation, NetworkSpecSHA256: session.NetworkSpecHash,
			CreatedAt: session.CreatedAt, UpdatedAt: session.UpdatedAt,
			LastHeartbeatAt: session.LastHeartbeatAt, ExpiresAt: session.ExpiresAt,
		})
	}
	api.audit(request, subjectFromRequest(request), "admin.session/list", "success")
	writeJSON(writer, http.StatusOK, listResponse("sessions", items, sessions, hasMore))
}

func (api *readAPI) listTasks(writer http.ResponseWriter, request *http.Request) {
	parameters, err := parseListParameters(request, "tasks", "principalId", "sessionId", "namespace", "type", "state")
	principalID := strings.TrimSpace(parameters.query.Get("principalId"))
	sessionID := strings.TrimSpace(parameters.query.Get("sessionId"))
	namespace := strings.TrimSpace(parameters.query.Get("namespace"))
	taskType := strings.TrimSpace(parameters.query.Get("type"))
	state := remotetask.State(strings.TrimSpace(parameters.query.Get("state")))
	if err != nil || !validOptionalUUID(principalID) || !validOptionalUUID(sessionID) ||
		!validOptionalNamespace(namespace) || len(taskType) > 128 || strings.ContainsAny(taskType, "\x00\r\n") ||
		(state != "" && !state.Valid()) {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	if !api.authorizeScopedList(request, adminauthorization.ResourceTask, namespace) {
		writeListError(writer, request, http.StatusForbidden, "forbidden", "management operation is not permitted")
		return
	}
	tasks, err := api.status.Tasks().List(request.Context(), storage.TaskListFilter{
		PrincipalID: principalID, SessionID: sessionID, Namespace: namespace,
		Type: taskType, State: state,
		Cursor: parameters.cursor, Limit: parameters.limit + 1,
	})
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.task/list", "failure")
		writeListError(writer, request, http.StatusServiceUnavailable, "unavailable", "management task list is unavailable")
		return
	}
	hasMore := len(tasks) > parameters.limit
	if hasMore {
		tasks = tasks[:parameters.limit]
	}
	items := make([]taskDocument, 0, len(tasks))
	for _, task := range tasks {
		items = append(items, taskDocument{
			ID: task.ID, PrincipalID: task.PrincipalID, SessionID: task.SessionID,
			Type: task.Type, State: string(task.State), CreatedAt: task.CreatedAt, UpdatedAt: task.UpdatedAt, ExpiresAt: task.ExpiresAt,
		})
	}
	api.audit(request, subjectFromRequest(request), "admin.task/list", "success")
	writeJSON(writer, http.StatusOK, listResponse("tasks", items, tasks, hasMore))
}

func (api *readAPI) listAudit(writer http.ResponseWriter, request *http.Request) {
	parameters, err := parseListParameters(request, "audit", "principalId", "action", "after", "before")
	principalID := strings.TrimSpace(parameters.query.Get("principalId"))
	action := strings.TrimSpace(parameters.query.Get("action"))
	if err != nil || !validOptionalUUID(principalID) || len(action) > 256 || strings.ContainsAny(action, "\x00\r\n") {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	filter := storage.AuditFilter{
		PrincipalID: principalID, Action: action,
		Cursor: parameters.cursor, Limit: parameters.limit + 1,
	}
	if filter.After, err = optionalTime(parameters.query.Get("after")); err != nil {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	if filter.Before, err = optionalTime(parameters.query.Get("before")); err != nil ||
		(!filter.After.IsZero() && !filter.Before.IsZero() && !filter.Before.After(filter.After)) {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	events, err := api.status.Audit().List(request.Context(), filter)
	if err != nil {
		api.audit(request, subjectFromRequest(request), "admin.audit/list", "failure")
		writeListError(writer, request, http.StatusServiceUnavailable, "unavailable", "management audit list is unavailable")
		return
	}
	hasMore := len(events) > parameters.limit
	if hasMore {
		events = events[:parameters.limit]
	}
	items := make([]auditDocument, 0, len(events))
	for _, event := range events {
		items = append(items, auditDocument{
			ID: event.ID, PrincipalID: event.PrincipalID, Action: event.Action,
			ResourceType: event.ResourceType, ResourceID: event.ResourceID,
			Outcome: event.Outcome, RequestID: event.RequestID, CreatedAt: event.CreatedAt,
		})
	}
	api.audit(request, subjectFromRequest(request), "admin.audit/list", "success")
	writeJSON(writer, http.StatusOK, listResponse("audit", items, events, hasMore))
}

func (api *readAPI) listRelays(writer http.ResponseWriter, request *http.Request) {
	limit, after, state, online, err := parseRelayListParameters(request)
	if err != nil {
		writeListError(writer, request, http.StatusBadRequest, "invalid_request", "management list query is invalid")
		return
	}
	statuses := []relayregistry.RelayStatus(nil)
	if api.relays != nil {
		statuses = api.relays.Snapshot()
	}
	slices.SortFunc(statuses, func(left, right relayregistry.RelayStatus) int {
		return strings.Compare(left.RelayID, right.RelayID)
	})
	items := make([]relayDocument, 0, min(limit, len(statuses)))
	var lastID string
	hasMore := false
	for _, status := range statuses {
		if status.RelayID <= after || state != "" && status.State != state || online != nil && status.Online != *online {
			continue
		}
		if len(items) == limit {
			hasMore = true
			break
		}
		items = append(items, relayDocument{
			RelayID: status.RelayID, State: status.State, DesiredState: status.DesiredState,
			Capacity: status.Capacity, AppliedKeyGeneration: status.AppliedKeyGeneration,
			AppliedRevocationGeneration: status.AppliedRevocationGeneration,
			LeaseExpiresAt:              status.LeaseExpiresAt, LastHeartbeatAt: status.LastHeartbeatAt,
			Reservations: status.Reservations, Online: status.Online,
		})
		lastID = status.RelayID
	}
	response := map[string]any{"items": items}
	if hasMore {
		response["nextCursor"] = encodeRelayCursor(lastID)
	}
	api.audit(request, subjectFromRequest(request), "admin.relay/list", "success")
	writeJSON(writer, http.StatusOK, response)
}

func (api *readAPI) authorizeScopedList(request *http.Request, resource adminauthorization.Resource, namespace string) bool {
	subject := subjectFromRequest(request)
	permission := adminauthorization.Request{Resource: resource, Operation: adminauthorization.OperationList, Namespace: namespace}
	decision := api.authorizer.Authorize(request.Context(), subject, permission)
	if !decision.Allowed {
		api.audit(request, subject, permission.Key(), "forbidden")
	}
	return decision.Allowed
}

func parseListParameters(request *http.Request, kind string, allowed ...string) (listParameters, error) {
	query := request.URL.Query()
	allowedKeys := map[string]struct{}{"limit": {}, "cursor": {}}
	for _, key := range allowed {
		allowedKeys[key] = struct{}{}
	}
	for key, values := range query {
		if _, ok := allowedKeys[key]; !ok || len(values) != 1 {
			return listParameters{}, errors.New("unsupported or repeated list query parameter")
		}
	}
	limit := defaultReadPageSize
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maximumReadPageSize {
			return listParameters{}, errors.New("list limit is invalid")
		}
		limit = parsed
	}
	var cursor *storage.PageCursor
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		decoded, err := decodeCursor(kind, raw)
		if err != nil {
			return listParameters{}, err
		}
		cursor = decoded
	}
	return listParameters{limit: limit, cursor: cursor, query: query}, nil
}

func encodeCursor(kind string, createdAt time.Time, id string) string {
	encoded, _ := json.Marshal(cursorDocument{Version: 1, Kind: kind, CreatedAt: createdAt.UTC(), ID: id})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeCursor(kind, raw string) (*storage.PageCursor, error) {
	if len(raw) > maximumCursorBytes {
		return nil, errors.New("management cursor is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > maximumCursorBytes {
		return nil, errors.New("management cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var document cursorDocument
	if err := decoder.Decode(&document); err != nil {
		return nil, errors.New("management cursor is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || document.Version != 1 ||
		document.Kind != kind || document.CreatedAt.IsZero() {
		return nil, errors.New("management cursor is invalid")
	}
	if _, err := uuid.Parse(document.ID); err != nil {
		return nil, errors.New("management cursor is invalid")
	}
	return &storage.PageCursor{CreatedAt: document.CreatedAt.UTC(), ID: document.ID}, nil
}

func listResponse[T any, R interface{ ~[]E }, E any](kind string, items T, rows R, hasMore bool) map[string]any {
	response := map[string]any{"items": items}
	if hasMore && len(rows) > 0 {
		var createdAt time.Time
		var id string
		switch value := any(rows[len(rows)-1]).(type) {
		case storage.Principal:
			createdAt, id = value.CreatedAt, value.ID
		case storage.Session:
			createdAt, id = value.CreatedAt, value.ID
		case storage.Task:
			createdAt, id = value.CreatedAt, value.ID
		case storage.AuditEvent:
			createdAt, id = value.CreatedAt, value.ID
		}
		response["nextCursor"] = encodeCursor(kind, createdAt, id)
	}
	return response
}

func optionalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, errors.New("management list timestamp is invalid")
	}
	return value.UTC(), nil
}

func writeListError(writer http.ResponseWriter, request *http.Request, status int, code, message string) {
	writeError(writer, status, code, message, requestID(request))
}

func validOptionalUUID(value string) bool {
	if value == "" {
		return true
	}
	_, err := uuid.Parse(value)
	return err == nil
}

func validOptionalNamespace(value string) bool {
	return value == "" || listNamespacePattern.MatchString(value)
}

func parseRelayListParameters(request *http.Request) (int, string, relaycontrol.State, *bool, error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "limit" && key != "cursor" && key != "state" && key != "online" || len(values) != 1 {
			return 0, "", "", nil, errors.New("unsupported or repeated Relay list query parameter")
		}
	}
	limit := defaultReadPageSize
	if raw := strings.TrimSpace(query.Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > maximumReadPageSize {
			return 0, "", "", nil, errors.New("Relay list limit is invalid")
		}
		limit = parsed
	}
	after := ""
	if raw := strings.TrimSpace(query.Get("cursor")); raw != "" {
		var err error
		if after, err = decodeRelayCursor(raw); err != nil {
			return 0, "", "", nil, err
		}
	}
	state := relaycontrol.State(strings.TrimSpace(query.Get("state")))
	if state != "" && state != relaycontrol.StateReady && state != relaycontrol.StateDraining {
		return 0, "", "", nil, errors.New("Relay list state is invalid")
	}
	var online *bool
	if raw := strings.TrimSpace(query.Get("online")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			return 0, "", "", nil, errors.New("Relay list online filter is invalid")
		}
		online = &parsed
	}
	return limit, after, state, online, nil
}

func encodeRelayCursor(relayID string) string {
	encoded, _ := json.Marshal(relayCursorDocument{Version: 1, Kind: "relays", RelayID: relayID})
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeRelayCursor(raw string) (string, error) {
	if len(raw) > maximumCursorBytes {
		return "", errors.New("management Relay cursor is invalid")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(decoded) > maximumCursorBytes {
		return "", errors.New("management Relay cursor is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(decoded))
	decoder.DisallowUnknownFields()
	var document relayCursorDocument
	if err := decoder.Decode(&document); err != nil {
		return "", errors.New("management Relay cursor is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) || document.Version != 1 ||
		document.Kind != "relays" || document.RelayID == "" || len(document.RelayID) > 256 ||
		strings.TrimSpace(document.RelayID) != document.RelayID || strings.ContainsAny(document.RelayID, "\x00\r\n") {
		return "", errors.New("management Relay cursor is invalid")
	}
	return document.RelayID, nil
}
