package kubeapi_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/kubeapi"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

type inventoryProvider struct{ client kubernetes.Interface }

func (provider inventoryProvider) ClientFor(authorization.Subject) (kubernetes.Interface, error) {
	return provider.client, nil
}

func TestInventoryWatchHTTPStreamsAuthorizedResyncSnapshots(t *testing.T) {
	client := fake.NewSimpleClientset(&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-0", Namespace: "development"}})
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "watch", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"watch"}, ResourceKinds: []string{"pods"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := kubeapi.New(inventoryProvider{client: client}, kubeapi.WithCapabilityAuthorizer(policy), kubeapi.WithInventoryResync(20*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	server, err := controller.NewServer(
		controller.Config{PublicURL: "https://gateway.example.test"}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(*http.Request) (controller.Principal, *controller.APIError) {
			return controller.Principal{Subject: "principal-1"}, nil
		})),
		controller.WithAuthorizer(policy), controller.WithAPIHandler(handler),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewTLSServer(server.Handler())
	defer httpServer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	connection, _, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(httpServer.URL, "https")+"/api/v2/namespaces/development/pods?watch=true", &websocket.DialOptions{
		HTTPClient: httpServer.Client(), CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	_, encoded, err := connection.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Resource  string `json:"resource"`
		Namespace string `json:"namespace"`
		Pods      []struct {
			Name string `json:"name"`
		} `json:"pods"`
	}
	if err := json.Unmarshal(encoded, &snapshot); err != nil || snapshot.Resource != "pods" || snapshot.Namespace != "development" ||
		len(snapshot.Pods) != 1 || snapshot.Pods[0].Name != "api-0" {
		t.Fatalf("snapshot = %#v, error = %v", snapshot, err)
	}
}
