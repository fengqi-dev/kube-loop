package main

import (
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/oauthserver"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func discoveryAuthMethods(registry *authn.Registry) []controlplane.AuthMethod {
	descriptors := registry.Descriptors()
	methods := make([]controlplane.AuthMethod, 0, len(descriptors))
	for _, descriptor := range descriptors {
		methods = append(methods, controlplane.AuthMethod{
			ID: descriptor.ID, Type: string(descriptor.Type),
			DisplayName: descriptor.DisplayName, Interaction: string(descriptor.Interaction),
		})
	}
	return methods
}

func authenticateWithFosite(endpoints *oauthserver.Endpoints) controlplaneapi.AuthenticatorFunc {
	return func(request *http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
		parts := strings.Fields(request.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required"}
		}
		identity, err := endpoints.Authenticate(request.Context(), parts[1])
		if err != nil {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "access token is invalid"}
		}
		principal := identity.Principal
		return controlplaneapi.Principal{Subject: principal.ID, Provider: principal.Provider, DisplayName: principal.DisplayName,
			Email: principal.Email, Groups: append([]string(nil), principal.Groups...), DeviceID: identity.DeviceID,
			AuthorizationID: identity.AuthorizationID, AccessExpiresAt: identity.AccessExpiresAt}, nil
	}
}
