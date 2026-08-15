package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"

	adminbootstrap "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/bootstrap"
	adminlocaluser "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/localuser"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/labstack/echo/v5"
)

func (api *readAPI) iamRoutes(group *echo.Group) {
	group.GET("/organizations", api.listOrganizations)
	group.GET("/organizations/:organizationID", api.getOrganization)
	group.GET("/organizations/:organizationID/invitations", api.listInvitations)
	group.POST("/organizations/:organizationID/invitations", api.createInvitation)
	group.DELETE("/organizations/:organizationID/invitations/:invitationID", api.revokeInvitation)
	group.GET("/organizations/:organizationID/groups", api.listGroups)
	group.POST("/organizations/:organizationID/groups", api.createGroup)
	group.PUT("/organizations/:organizationID/groups/:groupID", api.updateGroup)
	group.DELETE("/organizations/:organizationID/groups/:groupID", api.deleteGroup)
	group.GET("/organizations/:organizationID/groups/:groupID/memberships", api.listGroupMemberships)
	group.POST("/organizations/:organizationID/groups/:groupID/memberships", api.addGroupMembership)
	group.DELETE("/organizations/:organizationID/groups/:groupID/memberships/:identityID", api.removeGroupMembership)
	group.GET("/organizations/:organizationID/groups/:groupID/namespaces", api.listGroupNamespaces)
	group.POST("/organizations/:organizationID/groups/:groupID/namespaces", api.addGroupNamespace)
	group.DELETE("/organizations/:organizationID/groups/:groupID/namespaces/:namespace", api.removeGroupNamespace)
}

type completeBootstrapRequest struct {
	Token       string `json:"token"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
}

func (api *readAPI) completeBootstrap(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	requestID := ensureRequestID(writer, request)
	var input completeBootstrapRequest
	if !decodePolicyJSON(writer, request, &input) {
		return nil
	}
	result, err := api.bootstrapService.Complete(request.Context(), adminbootstrap.CompleteRequest{
		Token: input.Token, Username: input.Username, Password: []byte(input.Password),
		DisplayName: input.DisplayName, Email: input.Email, RequestID: requestID,
	})
	input.Password = ""
	if err != nil {
		status, code, message := http.StatusBadRequest, "invalid_request", "bootstrap request is invalid"
		switch {
		case errors.Is(err, adminbootstrap.ErrInvalidToken), errors.Is(err, adminbootstrap.ErrAlreadyCompleted):
			status, code, message = http.StatusUnauthorized, "invalid_bootstrap_token", "bootstrap token is invalid or expired"
		case errors.Is(err, adminlocaluser.ErrInvalidInput):
			message = "identity or password is invalid"
		case errors.Is(err, storage.ErrConflict):
			status, code, message = http.StatusConflict, "conflict", "identity or organization already exists"
		}
		writeError(writer, status, code, message, requestID)
		return nil
	}
	if api.authorizationReloader != nil {
		if err := api.authorizationReloader.Load(request.Context()); err != nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable", "IAM policy reload failed", requestID)
			return nil
		}
	}
	writeJSON(writer, http.StatusCreated, map[string]any{
		"identity": map[string]string{"id": result.Identity.IdentityID, "displayName": result.Identity.DisplayName,
			"email": result.Identity.Email},
		"organization": map[string]string{"id": result.Organization.ID, "name": result.Organization.Name,
			"slug": result.Organization.Slug},
	})
	return nil
}

func (api *readAPI) identityGroupIDs(ctx context.Context, identityID string) ([]string, error) {
	organizations, err := api.status.Organizations().ListForIdentity(ctx, identityID)
	if err != nil {
		return nil, err
	}
	groups := make([]string, 0)
	for _, organization := range organizations {
		items, listErr := api.status.Groups().ListForIdentity(ctx, organization.ID, identityID)
		if listErr != nil {
			return nil, listErr
		}
		for _, group := range items {
			groups = append(groups, group.ID)
		}
	}
	slices.Sort(groups)
	return slices.Compact(groups), nil
}

func decodePolicyJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	mediaType, _, err := mime.ParseMediaType(request.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(writer, http.StatusUnsupportedMediaType, "invalid_content_type", "application/json is required", requestID(request))
		return false
	}
	request.Body = http.MaxBytesReader(writer, request.Body, 64<<10)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid", requestID(request))
		return false
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(writer, http.StatusBadRequest, "invalid_request", "request body is invalid", requestID(request))
		return false
	}
	return true
}

func validChangeReason(value string) bool {
	value = strings.TrimSpace(value)
	return len(value) >= 8 && len(value) <= 512 && !strings.ContainsAny(value, "\x00\r\n")
}
