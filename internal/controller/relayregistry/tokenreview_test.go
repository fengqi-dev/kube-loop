package relayregistry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	authenticationv1 "k8s.io/api/authentication/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

func TestTokenReviewAuthenticatorUsesAudienceAndBoundPodUID(t *testing.T) {
	client := fake.NewSimpleClientset()
	client.Fake.PrependReactor("create", "tokenreviews", func(action ktesting.Action) (bool, runtime.Object, error) {
		review := action.(ktesting.CreateAction).GetObject().(*authenticationv1.TokenReview)
		if len(review.Spec.Audiences) != 1 || review.Spec.Audiences[0] != "kubeloop-relay" || review.Spec.Token != "projected-token" {
			t.Fatalf("TokenReview = %#v", review.Spec)
		}
		return true, &authenticationv1.TokenReview{Status: authenticationv1.TokenReviewStatus{
			Authenticated: true, Audiences: []string{"kubeloop-relay"},
			User: authenticationv1.UserInfo{
				Username: "system:serviceaccount:kubeloop:gateway",
				Extra:    map[string]authenticationv1.ExtraValue{podUIDExtra: {"pod-uid"}},
			},
		}}, nil
	})
	authenticator, err := NewTokenReviewAuthenticator(client, TokenReviewConfig{
		Audience: "kubeloop-relay", Namespace: "kubeloop", ServiceAccount: "gateway", TrustDomain: "cluster.local",
		TopologyResolver: func(_ context.Context, identity relaycontrol.PeerIdentity) (map[string]string, error) {
			if identity.PodUID != "pod-uid" {
				t.Fatalf("identity = %#v", identity)
			}
			return map[string]string{TopologyZone: "cn-a"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://controller/internal/v1/relays/register", nil)
	request.Header.Set("Authorization", "Bearer projected-token")
	identity, err := authenticator.Authenticate(request)
	if err != nil {
		t.Fatal(err)
	}
	if identity.PodUID != "pod-uid" || identity.Topology[TopologyZone] != "cn-a" {
		t.Fatalf("identity = %#v", identity)
	}
}
