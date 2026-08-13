package main

import (
	"net/http"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authn/token"
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

func authenticateWithTokens(tokenService *token.Service) controlplaneapi.AuthenticatorFunc {
	return func(request *http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
		headers := request.Header.Values("Authorization")
		if len(headers) != 1 {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			}
		}
		parts := strings.Fields(headers[0])
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "authentication required",
			}
		}
		identity, err := tokenService.Authenticate(request.Context(), parts[1])
		if err != nil {
			return controlplaneapi.Principal{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeUnauthenticated, Message: "access token is invalid",
			}
		}
		principal := identity.Principal
		return controlplaneapi.Principal{
			Subject: principal.ID, Provider: principal.Provider,
			DisplayName: principal.DisplayName, Email: principal.Email,
			Groups:   append([]string(nil), principal.Groups...),
			DeviceID: identity.DeviceID, FamilyID: identity.FamilyID,
			AccessExpiresAt: identity.AccessExpiresAt,
		}, nil
	}
}
