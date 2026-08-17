package main

import (
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func authenticateWithFosite(endpoints *oauthserver.Endpoints) controlplaneapi.AuthenticatorFunc {
	return func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return controlplaneapi.Identity{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required"}
		}
		accessIdentity, err := endpoints.Authenticate(request.Context(), parts[1])
		if err != nil {
			return controlplaneapi.Identity{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "access token is invalid"}
		}
		identity := accessIdentity.Identity
		return controlplaneapi.Identity{Subject: identity.ID, Provider: accessIdentity.ProviderID, DisplayName: identity.DisplayName,
			Email: identity.PrimaryEmail, Groups: append([]string(nil), accessIdentity.Groups...), DeviceID: accessIdentity.DeviceID,
			AuthorizationID: accessIdentity.AuthorizationID, AccessExpiresAt: accessIdentity.AccessExpiresAt}, nil
	}
}
