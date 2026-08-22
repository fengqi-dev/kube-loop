package kubeapi

import (
	"context"

	"github.com/labstack/echo/v5"
	authorizationv1 "k8s.io/api/authorization/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
)

func (handler *Service) capabilities(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	identity controlplaneapi.Identity,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	snapshot, apiError := handler.discoverCapabilities(
		request.Context(),
		client,
		identity,
		namespace,
	)
	if apiError != nil {
		return apiError
	}
	writeJSON(ctx, snapshot)
	return nil
}

// DiscoverCapabilities reports the Kubernetes capabilities of the signed-in identity.
func (handler *Service) DiscoverCapabilities(
	ctx context.Context,
	identity controlplaneapi.Identity,
	namespace string,
) (capability.Snapshot, *controlplaneapi.Error) {
	client, err := handler.provider.ClientFor(authorization.Subject{
		ID: identity.Subject, Provider: identity.Provider, Groups: append([]string(nil), identity.Groups...),
	})
	if err != nil {
		return capability.Snapshot{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeUnavailable, Message: kubernetesAPIUnavailableMessage, Cause: err,
		}
	}
	return handler.discoverCapabilities(ctx, client, identity, namespace)
}

func (handler *Service) discoverCapabilities(
	ctx context.Context,
	client kubernetesclient.Interface,
	identity controlplaneapi.Identity,
	namespace string,
) (capability.Snapshot, *controlplaneapi.Error) {
	type candidate struct {
		capability string
		kubernetes []authorizationv1.ResourceAttributes
	}
	namespaced := func(attributes ...authorizationv1.ResourceAttributes) []authorizationv1.ResourceAttributes {
		for index := range attributes {
			attributes[index].Namespace = namespace
		}
		return attributes
	}
	candidates := []candidate{
		{
			capability: "pods.get",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationGet,
					Resource: string(inventoryPods),
				},
			),
		},
		{
			capability: "pods.list",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationList,
					Resource: string(inventoryPods),
				},
			),
		},
		{
			capability: "pods.watch",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationWatch,
					Resource: string(inventoryPods),
				},
			),
		},
		{
			capability: "services.get",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationGet,
					Resource: string(inventoryServices),
				},
			),
		},
		{
			capability: "services.list",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationList,
					Resource: string(inventoryServices),
				},
			),
		},
		{
			capability: "services.watch",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:     operationWatch,
					Resource: string(inventoryServices),
				},
			),
		},
		{capability: "cluster.tunnel", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{
				Verb:     operationList,
				Resource: string(inventoryPods),
			},
			authorizationv1.ResourceAttributes{
				Verb:     operationList,
				Resource: string(inventoryServices),
			},
		)},
		{capability: "ports.forward", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: string(inventoryPods),
			},
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: string(inventoryServices),
			},
		)},
		{
			capability: "pods.exec",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:        operationCreate,
					Resource:    string(inventoryPods),
					Subresource: subresourceExec,
				},
			),
		},
		{
			capability: "pods.files",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:        operationCreate,
					Resource:    string(inventoryPods),
					Subresource: subresourceExec,
				},
			),
		},
		{
			capability: "pods.files.manage",
			kubernetes: namespaced(
				authorizationv1.ResourceAttributes{
					Verb:        operationCreate,
					Resource:    string(inventoryPods),
					Subresource: subresourceExec,
				},
			),
		},
		{capability: "services.exchange", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: string(inventoryServices),
			},
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: "endpoints",
			},
			authorizationv1.ResourceAttributes{
				Group:    "discovery.k8s.io",
				Verb:     operationList,
				Resource: "endpointslices",
			},
		)},
		{capability: "services.mirror", kubernetes: namespaced(
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: string(inventoryServices),
			},
			authorizationv1.ResourceAttributes{
				Verb:     operationGet,
				Resource: "endpoints",
			},
			authorizationv1.ResourceAttributes{
				Group:    "discovery.k8s.io",
				Verb:     operationList,
				Resource: "endpointslices",
			},
		)},
		{capability: "services.preview"},
	}
	capabilities := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		kubernetesAllowed := true
		for _, attributes := range candidate.kubernetes {
			review, err := client.AuthorizationV1().
				SelfSubjectAccessReviews().
				Create(ctx, &authorizationv1.SelfSubjectAccessReview{
					Spec: authorizationv1.SelfSubjectAccessReviewSpec{
						ResourceAttributes: &attributes,
					},
				}, metav1.CreateOptions{})
			if err != nil {
				return capability.Snapshot{}, mapKubernetesError(err)
			}
			if !review.Status.Allowed {
				kubernetesAllowed = false
				break
			}
		}
		if kubernetesAllowed {
			capabilities = append(capabilities, candidate.capability)
		}
	}
	snapshot, err := capability.Normalize(capability.Snapshot{
		SchemaVersion: capability.SchemaVersion, IdentityID: identity.Subject, Namespace: namespace,
		GatewayVersion: handler.gatewayVersion, Capabilities: capabilities,
	})
	if err != nil {
		return capability.Snapshot{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInternal, Message: "capability snapshot validation failed", Cause: err,
		}
	}
	return snapshot, nil
}
