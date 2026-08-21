package relayregistry

import (
	"errors"
	"net/http"
	"slices"
	"strings"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
)

const podUIDExtra = "authentication.kubernetes.io/pod-uid"

type TokenReviewConfig struct {
	Audience         string
	Namespace        string
	ServiceAccount   string
	TrustDomain      string
	TopologyResolver TopologyResolver
}

type TokenReviewAuthenticator struct {
	client kubernetes.Interface
	config TokenReviewConfig
}

func NewTokenReviewAuthenticator(
	client kubernetes.Interface,
	config TokenReviewConfig,
) (*TokenReviewAuthenticator, error) {
	probe := relaycontrol.PeerIdentity{
		TrustDomain: strings.TrimSpace(
			config.TrustDomain,
		), Namespace: strings.TrimSpace(config.Namespace),
		ServiceAccount: strings.TrimSpace(
			config.ServiceAccount,
		), PodUID: "probe",
	}
	config.Audience = strings.TrimSpace(config.Audience)
	config.Namespace = probe.Namespace
	config.ServiceAccount = probe.ServiceAccount
	config.TrustDomain = probe.TrustDomain
	if client == nil || config.Audience == "" || len(config.Audience) > 256 ||
		probe.Validate() != nil {
		return nil, errors.New(
			"relay TokenReview identity configuration is invalid",
		)
	}
	return &TokenReviewAuthenticator{client: client, config: config}, nil
}

func (authenticator *TokenReviewAuthenticator) Authenticate(
	request *http.Request,
) (relaycontrol.PeerIdentity, error) {
	if authenticator == nil || request == nil {
		return relaycontrol.PeerIdentity{}, errors.New(
			"relay TokenReview authenticator is unavailable",
		)
	}
	headers := request.Header.Values("Authorization")
	if len(headers) != 1 {
		return relaycontrol.PeerIdentity{}, errors.New(
			"one Relay workload bearer token is required",
		)
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") ||
		len(parts[1]) > 16<<10 {
		return relaycontrol.PeerIdentity{}, errors.New(
			"relay workload bearer token is invalid",
		)
	}
	review, err := authenticator.client.AuthenticationV1().
		TokenReviews().
		Create(
			request.Context(),
			&authenticationv1.TokenReview{
				Spec: authenticationv1.TokenReviewSpec{
					Token: parts[1], Audiences: []string{authenticator.config.Audience},
				},
			},
			metav1.CreateOptions{},
		)
	if err != nil || review == nil || !review.Status.Authenticated ||
		!containsString(
			review.Status.Audiences,
			authenticator.config.Audience,
		) {
		return relaycontrol.PeerIdentity{}, errors.New(
			"relay workload token was not authenticated",
		)
	}
	wantUsername := "system:serviceaccount:" + authenticator.config.Namespace + ":" + authenticator.config.ServiceAccount
	if review.Status.User.Username != wantUsername {
		return relaycontrol.PeerIdentity{}, errors.New(
			"relay workload ServiceAccount is not allowed",
		)
	}
	podUIDs := review.Status.User.Extra[podUIDExtra]
	if len(podUIDs) != 1 || strings.TrimSpace(podUIDs[0]) == "" {
		return relaycontrol.PeerIdentity{}, errors.New(
			"relay workload token is not bound to one Pod",
		)
	}
	identity := relaycontrol.PeerIdentity{
		TrustDomain: authenticator.config.TrustDomain, Namespace: authenticator.config.Namespace,
		ServiceAccount: authenticator.config.ServiceAccount, PodUID: podUIDs[0],
	}
	if authenticator.config.TopologyResolver != nil {
		topology, err := authenticator.config.TopologyResolver(
			request.Context(),
			identity,
		)
		if err != nil {
			return relaycontrol.PeerIdentity{}, errors.New(
				"resolve authenticated Relay topology",
			)
		}
		identity.Topology = topology
	}
	if err := identity.Validate(); err != nil {
		return relaycontrol.PeerIdentity{}, err
	}
	return identity, nil
}

func containsString(values []string, target string) bool {
	return slices.Contains(values, target)
}
