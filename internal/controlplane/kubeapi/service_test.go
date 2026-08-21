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

	"github.com/labstack/echo/v5"
	"k8s.io/client-go/rest"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/kubeapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
)

func TestReadOnlyKubernetesRoutesUseIdentityAndStableDocuments(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(
		http.HandlerFunc(
			func(writer http.ResponseWriter, request *http.Request) {
				calls.Add(1)
				if request.Header.Get(
					"Impersonate-User",
				) != "gateway:identity-123" {
					t.Errorf(
						"Impersonate-User = %q",
						request.Header.Get("Impersonate-User"),
					)
				}
				if got := request.Header.Values("Impersonate-Group"); len(
					got,
				) != 1 ||
					got[0] != "k8s:developers" {
					t.Errorf("Impersonate-Group = %v", got)
				}
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/version":
					_, _ = writer.Write(
						[]byte(
							`{"gitVersion":"v1.31.4","platform":"linux/amd64"}`,
						),
					)
				case "/api/v1/namespaces":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"NamespaceList","metadata":{"resourceVersion":"42"},"items":[{"metadata":{"name":"development"},"status":{"phase":"Active"}}]}`,
						),
					)
				case "/api/v1/namespaces/development":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"development"},"status":{"phase":"Active"}}`,
						),
					)
				case "/api/v1/namespaces/development/pods":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"PodList","metadata":{"resourceVersion":"43"},"items":[{"metadata":{"name":"api-0","namespace":"development"},"spec":{"nodeName":"node-a","containers":[{"name":"api","ports":[{"name":"http","containerPort":8080,"protocol":"TCP"}]},{"name":"sidecar"}]},"status":{"phase":"Running","podIP":"10.1.2.3","conditions":[{"type":"Ready","status":"True"}]}}]}`,
						),
					)
				case "/api/v1/namespaces/development/pods/api-0":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"Pod","metadata":{"name":"api-0","namespace":"development"},"spec":{"containers":[{"name":"api"}]},"status":{"phase":"Running"}}`,
						),
					)
				case "/api/v1/namespaces/development/services":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"ServiceList","metadata":{"resourceVersion":"44"},"items":[{"metadata":{"name":"api","namespace":"development"},"spec":{"type":"ClusterIP","clusterIP":"10.96.0.10","ports":[{"name":"http","port":80,"protocol":"TCP","targetPort":8080}]}}]}`,
						),
					)
				case "/api/v1/namespaces/development/services/api":
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"Service","metadata":{"name":"api","namespace":"development"},"spec":{"type":"ClusterIP","clusterIP":"10.96.0.10","ports":[{"port":80,"protocol":"TCP"}]}}`,
						),
					)
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
						t.Errorf(
							"capability namespace = %q",
							attributes.Namespace,
						)
					}
					allowed := attributes.Resource != "services" ||
						attributes.Verb != "watch"
					if (attributes.Resource == "services" || attributes.Resource == "endpoints" || attributes.Resource == "endpointslices") &&
						(attributes.Verb == "create" || attributes.Verb == "update" || attributes.Verb == "delete") {
						allowed = false
					}
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"authorization.k8s.io/v1","kind":"SelfSubjectAccessReview","status":{"allowed":` + strconv.FormatBool(
								allowed,
							) + `}}`,
						),
					)
				default:
					http.NotFound(writer, request)
				}
			},
		),
	)
	defer upstream.Close()
	server := newServer(t, upstream.URL)

	tests := []struct {
		path string
		want []string
	}{
		{
			path: "/api/version",
			want: []string{
				`"gitVersion":"v1.31.4"`,
				`"gatewayVersion":"v2-test"`,
			},
		},
		{path: "/api/capabilities?namespace=development", want: []string{
			`"schemaVersion":1`,
			`"identityId":"identity-123"`,
			`"namespace":"development"`,
			`"gatewayVersion":"v2-test"`,
			`"capabilities":["pods.get","pods.list","pods.watch","services.get","services.list","cluster.tunnel","ports.forward","pods.exec","pods.files","pods.files.manage","services.exchange","services.mirror","services.preview"]`,
		}},
		{
			path: "/api/namespaces?limit=10",
			want: []string{`"name":"development"`},
		},
		{
			path: "/api/namespaces/development",
			want: []string{`"status":"Active"`},
		},
		{
			path: "/api/namespaces/development/pods",
			want: []string{
				`"name":"api-0"`,
				`"ready":true`,
				`"containers":["api","sidecar"]`,
				`"ports":[{"name":"http","port":8080,"protocol":"TCP"}]`,
			},
		},
		{
			path: "/api/namespaces/development/pods/api-0",
			want: []string{`"name":"api-0"`, `"phase":"Running"`},
		},
		{
			path: "/api/namespaces/development/services",
			want: []string{`"clusterIp":"10.96.0.10"`, `"targetPort":"8080"`},
		},
		{
			path: "/api/namespaces/development/services/api",
			want: []string{`"name":"api"`, `"port":80`},
		},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			server.Handler().ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"status = %d body = %s",
					response.Code,
					response.Body.String(),
				)
			}
			if response.Header().Get("Cache-Control") != "no-store" ||
				response.Header().Get(echo.HeaderXRequestID) == "" {
				t.Fatalf("security headers = %#v", response.Header())
			}
			for _, want := range test.want {
				if !strings.Contains(response.Body.String(), want) {
					t.Fatalf(
						"response missing %s: %s",
						want,
						response.Body.String(),
					)
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
	upstream := httptest.NewServer(
		http.HandlerFunc(
			func(http.ResponseWriter, *http.Request) { calls.Add(1) },
		),
	)
	defer upstream.Close()
	server := newServer(t, upstream.URL)
	tests := []struct {
		method string
		path   string
		status int
		field  string
	}{
		{
			method: http.MethodGet,
			path:   "/api/namespaces?limit=0",
			status: http.StatusBadRequest,
			field:  "limit",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces?limit=1&limit=2",
			status: http.StatusBadRequest,
			field:  "limit",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces?watch=true",
			status: http.StatusBadRequest,
			field:  "watch",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces?labelSelector=app%20in%20(",
			status: http.StatusBadRequest,
			field:  "labelSelector",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces?fieldSelector=metadata.name%3Ddevelopment%0A",
			status: http.StatusBadRequest,
			field:  "fieldSelector",
		},
		{
			method: http.MethodGet,
			path:   "/api/capabilities",
			status: http.StatusBadRequest,
			field:  "namespace",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces/Bad_Name/pods",
			status: http.StatusBadRequest,
			field:  "namespace",
		},
		{
			method: http.MethodGet,
			path:   "/api/namespaces/development/pods/",
			status: http.StatusNotFound,
		},
		{
			method: http.MethodPost,
			path:   "/api/namespaces",
			status: http.StatusNotFound,
		},
	}
	for _, test := range tests {
		response := httptest.NewRecorder()
		server.Handler().
			ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
		if response.Code != test.status {
			t.Fatalf(
				"%s %s status = %d body = %s",
				test.method,
				test.path,
				response.Code,
				response.Body.String(),
			)
		}
		if test.field != "" {
			var document struct {
				Error struct {
					Field string `json:"field"`
				} `json:"error"`
			}
			if err := json.NewDecoder(response.Body).Decode(&document); err != nil ||
				document.Error.Field != test.field {
				t.Fatalf(
					"error field = %q, decode error = %v",
					document.Error.Field,
					err,
				)
			}
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid requests reached Kubernetes %d times", calls.Load())
	}
}

func TestNamespaceInventoryForwardsValidatedSelectors(t *testing.T) {
	upstream := httptest.NewServer(
		http.HandlerFunc(
			func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				switch request.URL.Path {
				case "/api/v1/namespaces":
					if request.URL.Query().
						Get("labelSelector") !=
						"team=platform" ||
						request.URL.Query().
							Get("fieldSelector") !=
							"status.phase=Active" {
						t.Fatalf("selectors = %s", request.URL.RawQuery)
					}
					_, _ = writer.Write(
						[]byte(
							`{"apiVersion":"v1","kind":"NamespaceList","metadata":{"resourceVersion":"42"},"items":[{"metadata":{"name":"development","labels":{"team":"platform"}},"status":{"phase":"Active"}}]}`,
						),
					)
				default:
					http.NotFound(writer, request)
				}
			},
		),
	)
	defer upstream.Close()
	server := newServerWithPolicy(
		t,
		upstream.URL,
		authorization.NewAuthenticated(),
	)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, httptest.NewRequest(
		http.MethodGet,
		"/api/namespaces?labelSelector=team%3Dplatform&fieldSelector=status.phase%3DActive",
		nil,
	))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"name":"development"`) ||
		strings.Contains(response.Body.String(), "secret") {
		t.Fatalf(
			"filtered namespaces status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestNamespaceInventoryDoesNotApplyIAMAuthorization(t *testing.T) {
	upstream := httptest.NewServer(
		http.HandlerFunc(
			func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if request.URL.Path != "/api/v1/namespaces" {
					http.NotFound(writer, request)
					return
				}
				_, _ = writer.Write(
					[]byte(
						`{"apiVersion":"v1","kind":"NamespaceList","metadata":{"resourceVersion":"42"},"items":[{"metadata":{"name":"secret"},"status":{"phase":"Active"}}]}`,
					),
				)
			},
		),
	)
	defer upstream.Close()
	server := newServerWithPolicy(
		t,
		upstream.URL,
		authorization.NewAuthenticated(),
	)
	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/namespaces", nil))
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"name":"secret"`) {
		t.Fatalf(
			"namespace list status = %d body = %s",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestKubernetesErrorsAreSanitized(t *testing.T) {
	upstream := httptest.NewServer(
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write(
				[]byte(
					`{"apiVersion":"v1","kind":"Status","status":"Failure","reason":"Forbidden","message":"secret RBAC details","code":403}`,
				),
			)
		}),
	)
	defer upstream.Close()
	server := newServer(t, upstream.URL)
	response := httptest.NewRecorder()
	server.Handler().
		ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/namespaces", nil))
	if response.Code != http.StatusForbidden ||
		strings.Contains(response.Body.String(), "secret RBAC details") {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	if !strings.Contains(
		response.Body.String(),
		"Kubernetes operation is not permitted",
	) {
		t.Fatalf("unexpected error body: %s", response.Body.String())
	}
}

func newServer(t *testing.T, upstreamURL string) *controlplane.Server {
	t.Helper()
	return newServerWithPolicy(t, upstreamURL, authorization.NewAuthenticated())
}

func newServerWithPolicy(
	t *testing.T,
	upstreamURL string,
	policy authorization.Authorizer,
) *controlplane.Server {
	t.Helper()
	provider, err := controlplanekubernetes.NewForRESTConfig(
		&rest.Config{Host: upstreamURL},
		controlplanekubernetes.Config{
			Impersonation: controlplanekubernetes.ImpersonationConfig{
				Enabled: true, UsernamePrefix: "gateway:",
				GroupMappings: map[string][]string{
					"developers": {"k8s:developers"},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := kubeapi.New(
		provider,
		kubeapi.WithGatewayVersion("v2-test"),
	)
	if err != nil {
		t.Fatal(err)
	}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{
						Subject: "identity-123",
						Groups:  []string{"developers", "unmapped"},
					}, nil
				},
			),
		),
		controlplane.WithAuthorizer(
			policy,
		),
		controlplane.WithAPIRoutes(
			controlplane.APIRoutes{
				Kubernetes: kubeapi.NewRoutes(handler).Endpoints(),
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	return server
}
