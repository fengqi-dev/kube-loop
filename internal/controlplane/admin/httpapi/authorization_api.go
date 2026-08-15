package httpapi

import (
	"net/http"
	"slices"
	"strings"

	adminauthorization "github.com/fengqi-dev/kube-loop/internal/controlplane/admin/authorization"
	"github.com/labstack/echo/v5"
)

type effectiveCapabilityDocument struct {
	ID               string   `json:"id"`
	Scope            string   `json:"scope"`
	Allowed          bool     `json:"allowed"`
	Namespaces       []string `json:"namespaces,omitempty"`
	MatchingGroupIDs []string `json:"matchingGroupIds,omitempty"`
}

type effectiveAuthorizationDocument struct {
	IdentityID    string                        `json:"identityId"`
	Groups        []string                      `json:"groups"`
	Administrator bool                          `json:"administrator"`
	Namespaces    []string                      `json:"namespaces"`
	Capabilities  []effectiveCapabilityDocument `json:"capabilities"`
}

func (api *readAPI) effectiveAuthorization(ctx *echo.Context) error {
	writer, request := ctx.Response(), ctx.Request()
	subject := subjectFromRequest(request)
	administrator := api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{
		Capability: "platform.overview.read",
	}).Allowed
	namespaces := api.authorizer.AuthorizedNamespaces(subject)
	groups := slices.Clone(subject.Groups)
	if groups == nil {
		groups = []string{}
	}
	documents := make([]effectiveCapabilityDocument, 0, len(adminauthorization.Capabilities()))
	for _, capability := range adminauthorization.Capabilities() {
		document := effectiveCapabilityDocument{ID: string(capability), Scope: capabilityScope(capability)}
		matches := make([]string, 0)
		if document.Scope == "namespace" {
			document.Allowed = administrator || len(namespaces) > 0
			if administrator {
				document.Namespaces = []string{"*"}
				decision := api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{
					Capability: capability, Namespace: "default",
				})
				matches = appendDecisionGroups(matches, decision)
			} else if document.Allowed {
				document.Namespaces = slices.Clone(namespaces)
				for _, namespace := range namespaces {
					decision := api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{
						Capability: capability, Namespace: namespace,
					})
					matches = appendDecisionGroups(matches, decision)
				}
			}
		} else {
			decision := api.authorizer.Authorize(request.Context(), subject, adminauthorization.Request{Capability: capability})
			document.Allowed = decision.Allowed
			matches = appendDecisionGroups(matches, decision)
		}
		slices.Sort(matches)
		document.MatchingGroupIDs = slices.Compact(matches)
		documents = append(documents, document)
	}
	api.audit(request, subject, "admin.authorization/read", "success")
	writeJSON(writer, http.StatusOK, effectiveAuthorizationDocument{
		IdentityID: subject.ID, Groups: groups, Administrator: administrator,
		Namespaces: namespaces, Capabilities: documents,
	})
	return nil
}

func capabilityScope(capability adminauthorization.Capability) string {
	prefix, _, _ := strings.Cut(string(capability), ".")
	switch prefix {
	case "org":
		return "organization"
	case "namespace":
		return "namespace"
	default:
		return "platform"
	}
}

func appendDecisionGroups(values []string, decision adminauthorization.Decision) []string {
	for _, match := range decision.MatchingAllow {
		values = append(values, match.GroupID)
	}
	return values
}
