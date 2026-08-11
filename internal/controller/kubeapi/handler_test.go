package kubeapi_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/kubeapi"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"k8s.io/client-go/rest"
)

func TestReadOnlyKubernetesRoutesUsePrincipalAndStableDocuments(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Header.Get("Impersonate-User") != "gateway:principal-123" {
			t.Errorf("Impersonate-User = %q", request.Header.Get("Impersonate-User"))
		}
		if got := request.Header.Values("Impersonate-Group"); len(got) != 1 || got[0] != "k8s:developers" {
			t.Errorf("Impersonate-Group = %v", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/version":
			_, _ = writer.Write([]byte(`{"gitVersion":"v1.31.4","platform":"linux/amd64"}`))
		case "/api/v1/namespaces":
			if request.URL.Query().Get("limit") != "10" || request.URL.Query().Get("continue") != "next-page" {
				t.Errorf("namespace pagination query = %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","metadata":{"resourceVersion":"42","continue":"page-2"},"items":[{"metadata":{"name":"development"},"status":{"phase":"Active"}}]}`))
		case "/api/v1/namespaces/development":
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"development"},"status":{"phase":"Active"}}`))
		case "/api/v1/namespaces/development/pods":
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"43"},"items":[{"metadata":{"name":"api-0","namespace":"development"},"spec":{"nodeName":"node-a","containers":[{"name":"api","ports":[{"name":"http","containerPort":8080,"protocol":"TCP"}]},{"name":"sidecar"}]},"status":{"phase":"Running","podIP":"10.1.2.3","conditions":[{"type":"Ready","status":"True"}]}}]}`))
		case "/api/v1/namespaces/development/pods/api-0":
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api-0","namespace":"development"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running"}}`))
		case "/api/v1/namespaces/development/services":
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"ServiceList","metadata":{"resourceVersion":"44"},"items":[{"metadata":{"name":"api","namespace":"development"},"spec":{"type":"ClusterIP","clusterIP":"10.96.0.10","ports":[{"name":"http","port":80,"protocol":"TCP","targetPort":8080}]}}]}`))
		case "/api/v1/namespaces/development/services/api":
			_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"development"},"spec":{"type":"ClusterIP","clusterIP":"10.96.0.10","ports":[{"port":80,"protocol":"TCP"}]}}`))
		case "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews":
			var review struct {
				Spec struct {
					ResourceAttributes struct {
						Namespace string `json:"namespace"`
						Verb      string `json:"verb"`
						Resource  string `json:"resource"`
					} `json:"resourceAttributes"`
				} `json:"spec"`
			}
			if err := json.NewDecoder(request.Body).Decode(&review); err != nil {
				t.Errorf("decode access review: %v", err)
			}
			attributes := review.Spec.ResourceAttributes
			if attributes.Namespace != "development" {
				t.Errorf("capability namespace = %q", attributes.Namespace)
			}
			allowed := !(attributes.Resource == "services" && attributes.Verb == "watch")
			if (attributes.Resource == "services" || attributes.Resource == "endpoints" || attributes.Resource == "endpointslices") &&
				(attributes.Verb == "create" || attributes.Verb == "update" || attributes.Verb == "delete") {
				allowed = false
			}
			_, _ = writer.Write([]byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":` + strconv.FormatBool(allowed) + `}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer upstream.Close()
	server := newServer(t, upstream.URL)

	tests := []struct {
		path string
		want []string
	}{
		{path: "/api/v2/version", want: []string{`"gitVersion":"v1.31.4"`, `"gatewayVersion":"v2-test"`}},
		{path: "/api/v2/capabilities?namespace=development", want: []string{
			`"schemaVersion":1`, `"principalId":"principal-123"`, `"namespace":"development"`, `"gatewayVersion":"v2-test"`,
			`"capabilities":["pods.get","pods.list","pods.watch","services.get","services.list","cluster.tunnel","ports.forward","pods.exec","pods.files","pods.files.manage","services.exchange","services.mirror","services.preview"]`,
		}},
		{path: "/api/v2/namespaces?limit=10&continue=next-page", want: []string{`"name":"development"`, `"continue":"page-2"`, `"resourceVersion":"42"`}},
		{path: "/api/v2/namespaces/development", want: []string{`"status":"Active"`}},
		{path: "/api/v2/namespaces/development/pods", want: []string{`"name":"api-0"`, `"ready":true`, `"containers":["api","sidecar"]`, `"ports":[{"name":"http","port":8080,"protocol":"TCP"}]`}},
		{path: "/api/v2/namespaces/development/pods/api-0", want: []string{`"name":"api-0"`, `"phase":"Running"`}},
		{path: "/api/v2/namespaces/development/services", want: []string{`"clusterIp":"10.96.0.10"`, `"targetPort":"8080"`}},
		{path: "/api/v2/namespaces/development/services/api", want: []string{`"name":"api"`, `"port":80`}},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
			}
			if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get(controller.RequestIDHeader) == "" {
				t.Fatalf("security headers = %#v", response.Header())
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf("response missing %s: %s", want, response.Body.String())
				}
			}
		})
	}
	expectedCalls := int32(len(tests) + 18)
	if calls.Load() != expectedCalls {
		t.Fatalf("upstream calls = %d, want %d", calls.Load(), expectedCalls)
	}
}

func TestInvalidRoutesAndPaginationDoNotReachKubernetes(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls.Add(1) }))
	defer upstream.Close()
	server := newServer(t, upstream.URL)
	tests := []struct {
		method string
		path   string
		status int
		field  string
	}{
		{method: http.MethodGet, path: "/api/v2/namespaces?limit=0", status: http.StatusBadRequest, field: "limit"},
		{method: http.MethodGet, path: "/api/v2/namespaces?limit=1&limit=2", status: http.StatusBadRequest, field: "limit"},
		{method: http.MethodGet, path: "/api/v2/namespaces?watch=true", status: http.StatusBadRequest, field: "watch"},
		{method: http.MethodGet, path: "/api/v2/namespaces?labelSelector=app%20in%20(", status: http.StatusBadRequest, field: "labelSelector"},
		{method: http.MethodGet, path: "/api/v2/namespaces?fieldSelector=metadata.name%3Ddevelopment%0A", status: http.StatusBadRequest, field: "fieldSelector"},
		{method: http.MethodGet, path: "/api/v2/capabilities", status: http.StatusBadRequest, field: "namespace"},
		{method: http.MethodGet, path: "/api/v2/namespaces/Bad_Name/pods", status: http.StatusBadRequest, field: "namespace"},
		{method: http.MethodGet, path: "/api/v2/namespaces/development/pods/", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/v2/namespaces", status: http.StatusNotFound},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		server.Handler().ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Fatalf("%s %s status = %d body = %s", test.method, test.path, response.Code, response.Body.String())
		}
		if test.field != "" {
			var document struct {
				Error struct {
					Field string `json:"field"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&document); err != nil || document.Error.Field != test.field {
				t.Fatalf("error field = %q, decode error = %v", document.Error.Field, err)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached Kubernetes %d times", calls.Load())
	}
}

func TestNamespaceInventoryFiltersPolicyAndForwardsValidatedSelectors(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.URL.Path != "/api/v1/namespaces" {
			http.NotFound(writer, request)
			return
		}
		if request.URL.Query().Get("labelSelector") != "team=platform" ||
			request.URL.Query().Get("fieldSelector") != "status.phase=Active" {
			t.Errorf("selectors = %q, %q", request.URL.Query().Get("labelSelector"), request.URL.Query().Get("fieldSelector"))
		}
		_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"NamespaceList","metadata":{"resourceVersion":"42"},"items":[{"metadata":{"name":"development"},"status":{"phase":"Active"}},{"metadata":{"name":"secret"},"status":{"phase":"Active"}}]}`))
	}))
	defer upstream.Close()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{
		{ID: "namespace-candidates", Subjects: []string{"*"}, Namespaces: []string{"$cluster"}, Operations: []string{"list"}, ResourceKinds: []string{"namespaces"}},
		{ID: "development-inventory", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"list"}, ResourceKinds: []string{"capabilities"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServerWithPolicy(t, upstream.URL, policy)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/v2/namespaces?labelSelector=team%3Dplatform&fieldSelector=status.phase%3DActive",
		nil,
	))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"development"`) ||
		strings.Contains(response.Body.String(), "secret") {
		t.Fatalf("filtered namespaces status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestKubernetesErrorsAreSanitized(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = writer.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"secret RBAC details","code":403}`))
	}))
	defer upstream.Close()
	server := newServer(t, upstream.URL)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/namespaces", nil))
	if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "secret RBAC details") {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "Kubernetes operation is not permitted") {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
}

func TestStreamCapabilitiesRequireBothCreateAndStreamPolicyOperations(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`))
	}))
	defer upstream.Close()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{
		{ID: "capabilities", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"list"}, ResourceKinds: []string{"capabilities"}},
		{ID: "create-only", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"create"}, ResourceKinds: []string{"pod-exec", "file-transfers"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServerWithPolicy(t, upstream.URL, policy)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities?namespace=development", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "pods.exec") || strings.Contains(response.Body.String(), "pods.files") {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestFileManagementCapabilityRequiresEveryPolicyOperation(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`))
	}))
	defer upstream.Close()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{
		{ID: "capabilities", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"list"}, ResourceKinds: []string{"capabilities"}},
		{ID: "incomplete-files", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"list", "create", "update", "delete"}, ResourceKinds: []string{"pod-files"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServerWithPolicy(t, upstream.URL, policy)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities?namespace=development", nil))
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "pods.files.manage") {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
}

func TestTunnelAndPortForwardCapabilitiesRequireCompleteGatewayPolicy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews" {
			http.NotFound(writer, request)
			return
		}
		_, _ = writer.Write([]byte(`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":true}}`))
	}))
	defer upstream.Close()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{
		{ID: "capabilities", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"list"}, ResourceKinds: []string{"capabilities"}},
		{ID: "sessions", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"create", "get", "heartbeat", "delete"}, ResourceKinds: []string{"sessions"}},
		{ID: "incomplete-forward", Subjects: []string{"*"}, Namespaces: []string{"development"}, Operations: []string{"create", "list"}, ResourceKinds: []string{"port-forwards"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := newServerWithPolicy(t, upstream.URL, policy)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v2/capabilities?namespace=development", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "cluster.tunnel") || strings.Contains(response.Body.String(), "ports.forward") {
		t.Fatalf("incomplete workflow policy was advertised: %s", response.Body.String())
	}
}

func newServer(t *testing.T, upstreamURL string) *controller.Server {
	t.Helper()
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "test-all", Subjects: []string{"*"}, Namespaces: []string{"*"}, Operations: []string{"*"}, ResourceKinds: []string{"*"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	return newServerWithPolicy(t, upstreamURL, policy)
}

func newServerWithPolicy(t *testing.T, upstreamURL string, policy authorization.Authorizer) *controller.Server {
	t.Helper()
	provider, err := controllerkubernetes.NewForRESTConfig(&rest.Config{Host: upstreamURL}, controllerkubernetes.Config{
		Impersonation: controllerkubernetes.ImpersonationConfig{
			Enabled: true, UsernamePrefix: "gateway:",
			GroupMappings: map[string][]string{"developers": {"k8s:developers"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := kubeapi.New(
		provider,
		kubeapi.WithCapabilityAuthorizer(policy),
		kubeapi.WithGatewayVersion("v2-test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := controller.NewServer(
		controller.Config{PublicURL: "https://gateway.example.test"}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(*http.Request) (controller.Principal, *controller.APIError) {
			return controller.Principal{Subject: "principal-123", Groups: []string{"developers", "unmapped"}}, nil
		})),
		controller.WithAuthorizer(policy), controller.WithAPIHandler(handler),
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
