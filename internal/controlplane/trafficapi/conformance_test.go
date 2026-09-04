// Package trafficapi_test characterizes the HTTP and traffic-control contract
// that exchangeapi, mirrorapi and previewapi implement. The three packages
// carry near-identical handler bodies, so this suite drives all three through
// their public endpoints and asserts on the wire contract alone -- status
// codes, headers, JSON documents and error codes. It must keep passing
// unchanged while that duplication is factored out.
package trafficapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/exchangeapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/mirrorapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/previewapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
	"github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

const (
	testNamespace = "development"
	testSessionID = "11111111-1111-4111-8111-111111111111"
	testSubject   = "identity-a"
	testRelayID   = "relay-a"
	testService   = "api"
	unknownTaskID = "22222222-2222-4222-8222-222222222222"
)

// relayControl is the traffic-control handshake every traffic task API serves
// for the Gateway. All three services satisfy it structurally.
type relayControl interface {
	Claim(context.Context, string, trafficcontrol.ClaimRequest) (trafficcontrol.ClaimResponse, *controlplaneapi.Error)
	Prepare(
		context.Context,
		string,
		trafficcontrol.PrepareRequest,
	) (trafficcontrol.PrepareResponse, *controlplaneapi.Error)
	Heartbeat(
		context.Context,
		string,
		trafficcontrol.HeartbeatRequest,
	) (trafficcontrol.HeartbeatResponse, *controlplaneapi.Error)
	Finish(
		context.Context,
		string,
		trafficcontrol.FinishRequest,
	) (trafficcontrol.FinishResponse, *controlplaneapi.Error)
}

// probe counts the resource mutations a handler drove, so the same assertions
// work against the intercept mutator and the preview manager.
type probe struct {
	captures  int
	applies   int
	releases  int
	deletions int
}

type resources interface {
	counts() probe
	failRelease(error)
	bindings() *trafficbindingclient.Manager
}

type fixture struct {
	name        string
	taskType    string
	pathSegment string
	// serviceField is the JSON member naming the target Service. Exchange and
	// Mirror intercept an existing Service; Preview creates a new one.
	serviceField string
	// expiresAt reports whether the document exposes the Session expiry.
	expiresAt bool
	claimMode trafficcontrol.Mode
	// backends reports whether Prepare answers with the original backend sets.
	backends  bool
	endpoints controlplane.RemoteTaskEndpoints
	relay     relayControl
	resources resources
}

type fakeSessions struct {
	generation uint64
	expiresAt  time.Time
}

func (sessions fakeSessions) RequireActive(
	_ context.Context, _ controlplaneapi.Identity, namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if namespace != testNamespace || sessionID != testSessionID {
		return sessionapi.ActiveSession{}, controlplaneapi.NotFound()
	}
	return sessionapi.ActiveSession{
		ID: sessionID, Namespace: namespace,
		Generation: sessions.generation, ExpiresAt: sessions.expiresAt,
	}, nil
}

type fakeResolver struct{ err error }

func (resolver fakeResolver) ResolveService(
	_ context.Context, _ controlplaneapi.Identity, _, service string, ports []servicemodel.Port,
) (servicemodel.ResolvedService, error) {
	if resolver.err != nil {
		return servicemodel.ResolvedService{}, resolver.err
	}
	return servicemodel.ResolvedService{
		Name: service, ClusterIP: "10.96.0.20",
		Ports: append([]servicemodel.Port(nil), ports...),
	}, nil
}

// interceptResources stands in for exchangeapi.ResourceMutator and
// mirrorapi.ResourceMutator. It records the Kubernetes-facing mutations and
// exposes the real session store so the handlers read back what they wrote.
type interceptResources struct {
	manager    *trafficbindingclient.Manager
	seen       probe
	releaseErr error
}

func (fake *interceptResources) Capture(
	_ context.Context, _ controlplaneapi.Identity, snapshot *servicebinding.ServiceInterceptSnapshot,
) error {
	fake.seen.captures++
	snapshot.Selector = map[string]string{"app": snapshot.Service}
	// Mirror resolves the original backends out of the captured snapshot before
	// it answers Prepare, so the capture has to look like a real Service.
	ready, name, port, protocol := true, "http", int32(8080), corev1.ProtocolTCP
	snapshot.HasEndpointSlices = true
	snapshot.EndpointSlices = []discoveryv1.EndpointSlice{{
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints: []discoveryv1.Endpoint{{
			Addresses:  []string{"10.244.1.5"},
			Conditions: discoveryv1.EndpointConditions{Ready: &ready},
		}},
		Ports: []discoveryv1.EndpointPort{{
			Name: &name, Port: &port, Protocol: &protocol,
		}},
	}}
	return nil
}

func (fake *interceptResources) Apply(
	_ context.Context, _ controlplaneapi.Identity,
	_ servicebinding.ServiceInterceptSnapshot, _ string,
) error {
	fake.seen.applies++
	return nil
}

func (fake *interceptResources) Restore(
	_ context.Context, _ servicebinding.ServiceInterceptSnapshot, _ string,
) error {
	fake.seen.releases++
	return fake.releaseErr
}

func (fake *interceptResources) DeleteBinding(_ context.Context, _, _ string) error {
	fake.seen.deletions++
	return nil
}

func (fake *interceptResources) BindingManager() *trafficbindingclient.Manager { return fake.manager }

func (fake *interceptResources) counts() probe                           { return fake.seen }
func (fake *interceptResources) failRelease(err error)                   { fake.releaseErr = err }
func (fake *interceptResources) bindings() *trafficbindingclient.Manager { return fake.manager }

// previewResources stands in for previewapi.ResourceManager. Preview creates a
// Service rather than intercepting one, so Create counts as both the capture
// and the apply of the intercept flow.
type previewResources struct {
	manager    *trafficbindingclient.Manager
	seen       probe
	releaseErr error
}

func (fake *previewResources) Create(
	_ context.Context, _ controlplaneapi.Identity,
	snapshot servicebinding.PreviewServiceSnapshot, _ string,
) (*corev1.Service, error) {
	fake.seen.captures++
	fake.seen.applies++
	return &corev1.Service{
		Name: snapshot.Service, Namespace: snapshot.Namespace,
		Spec: corev1.ServiceSpec{ClusterIP: "10.96.0.30"},
	}, nil
}

func (fake *previewResources) Delete(
	_ context.Context, _ servicebinding.PreviewServiceSnapshot, _ string,
) error {
	fake.seen.releases++
	return fake.releaseErr
}

func (fake *previewResources) DeleteBinding(_ context.Context, _, _ string) error {
	fake.seen.deletions++
	return nil
}

func (fake *previewResources) BindingManager() *trafficbindingclient.Manager { return fake.manager }

func (fake *previewResources) counts() probe                           { return fake.seen }
func (fake *previewResources) failRelease(err error)                   { fake.releaseErr = err }
func (fake *previewResources) bindings() *trafficbindingclient.Manager { return fake.manager }

func newBindingManager(t *testing.T) *trafficbindingclient.Manager {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := trafficv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	kubernetesClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&trafficv1alpha1.TrafficBinding{}).Build()
	manager, err := trafficbindingclient.New(
		kubernetesClient, trafficbindingclient.Config{PollInterval: 10 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func newExchange(t *testing.T) fixture {
	t.Helper()
	store := &interceptResources{manager: newBindingManager(t)}
	service, err := exchangeapi.New(
		testSessionValidator(), fakeResolver{}, store,
		exchangeapi.Config{RestoreTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		name: "exchange", taskType: exchangeapi.TaskType, pathSegment: "exchanges",
		serviceField: "service", expiresAt: true, claimMode: trafficcontrol.ModeExchange,
		endpoints: exchangeapi.NewRoutes(service).Endpoints(),
		relay:     service, resources: store,
	}
}

func newMirror(t *testing.T) fixture {
	t.Helper()
	store := &interceptResources{manager: newBindingManager(t)}
	service, err := mirrorapi.New(
		testSessionValidator(), fakeResolver{}, store,
		mirrorapi.Config{RestoreTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		name: "mirror", taskType: mirrorapi.TaskType, pathSegment: "mirrors",
		serviceField: "service", expiresAt: true, claimMode: trafficcontrol.ModeMirror, backends: true,
		endpoints: mirrorapi.NewRoutes(service).Endpoints(),
		relay:     service, resources: store,
	}
}

func newPreview(t *testing.T) fixture {
	t.Helper()
	store := &previewResources{manager: newBindingManager(t)}
	service, err := previewapi.New(
		testSessionValidator(), store, previewapi.Config{DeleteTimeout: time.Second},
	)
	if err != nil {
		t.Fatal(err)
	}
	return fixture{
		name: "preview", taskType: previewapi.TaskType, pathSegment: "previews",
		serviceField: "name", expiresAt: false, claimMode: trafficcontrol.ModePreview,
		endpoints: previewapi.NewRoutes(service).Endpoints(),
		relay:     service, resources: store,
	}
}

func testSessionValidator() fakeSessions {
	return fakeSessions{
		generation: 1,
		expiresAt:  time.Date(2030, time.January, 2, 3, 4, 5, 0, time.UTC),
	}
}

func allFixtures() []func(*testing.T) fixture {
	return []func(*testing.T) fixture{newExchange, newMirror, newPreview}
}

func identity() controlplaneapi.Identity {
	return controlplaneapi.Identity{Subject: testSubject}
}

func ticketIdentity() trafficcontrol.Identity {
	return trafficcontrol.Identity{
		IdentityID: testSubject, SessionID: testSessionID,
		Namespace: testNamespace, SessionGeneration: 1,
	}
}

func newContext(method, target, body, idempotencyKey, taskID string) (*echo.Context, *httptest.ResponseRecorder) {
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	request := httptest.NewRequest(method, target, reader)
	request.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	if idempotencyKey != "" {
		request.Header.Set(sessionapi.IdempotencyHeader, idempotencyKey)
	}
	request.SetPathValue("sessionID", testSessionID)
	if taskID != "" {
		request.SetPathValue("taskID", taskID)
	}
	recorder := httptest.NewRecorder()
	return echo.New().NewContext(request, recorder), recorder
}

func (test fixture) specJSON(service string) string {
	return `{"` + test.serviceField + `":"` + service + `",` +
		`"ports":[{"name":"http","servicePort":8080,"protocol":"tcp"}],` +
		`"localTargets":[{"servicePort":8080,"protocol":"tcp","localPort":18080}]}`
}

func (test fixture) taskID(idempotencyKey string) string {
	return trafficbindingclient.TaskIDForIdempotency(
		testSessionID, test.taskType, testSubject, idempotencyKey,
	)
}

func (test fixture) create(
	t *testing.T, spec, idempotencyKey string,
) (*httptest.ResponseRecorder, *controlplaneapi.Error) {
	t.Helper()
	ctx, recorder := newContext(
		http.MethodPost, "/?namespace="+testNamespace, spec, idempotencyKey, "",
	)
	return recorder, test.endpoints.Create(ctx, identity())
}

func (test fixture) call(
	t *testing.T, endpoint controlplane.EndpointFunc, method, taskID string,
) (*httptest.ResponseRecorder, *controlplaneapi.Error) {
	t.Helper()
	ctx, recorder := newContext(method, "/?namespace="+testNamespace, "", "", taskID)
	return recorder, endpoint(ctx, identity())
}

func decode(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	document := map[string]any{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("response body %q is not a JSON document: %v", recorder.Body.String(), err)
	}
	return document
}

func TestCreatePersistsTheSessionAndReplaysIdempotently(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder, apiError := test.create(t, test.specJSON(testService), "key-1")
			if apiError != nil {
				t.Fatalf("create() error = %#v", apiError)
			}
			if recorder.Code != http.StatusCreated {
				t.Fatalf("create() status = %d, want %d", recorder.Code, http.StatusCreated)
			}
			taskID := test.taskID("key-1")
			wantLocation := controlplane.APIPathPrefix + "/sessions/" + testSessionID +
				"/" + test.pathSegment + "/" + taskID + "?namespace=" + testNamespace
			if location := recorder.Header().Get("Location"); location != wantLocation {
				t.Fatalf("create() Location = %q, want %q", location, wantLocation)
			}
			if replayed := recorder.Header().Get("Idempotent-Replayed"); replayed != "" {
				t.Fatalf("first create() reported a replay: %q", replayed)
			}
			document := decode(t, recorder)
			if document["id"] != taskID || document["sessionId"] != testSessionID ||
				document["namespace"] != testNamespace || document[test.serviceField] != testService {
				t.Fatalf("create() document = %#v", document)
			}
			if _, hasExpiry := document["expiresAt"]; hasExpiry != test.expiresAt {
				t.Fatalf("create() document expiresAt presence = %v, want %v", hasExpiry, test.expiresAt)
			}

			replay, apiError := test.create(t, test.specJSON(testService), "key-1")
			if apiError != nil {
				t.Fatalf("replayed create() error = %#v", apiError)
			}
			if replay.Code != http.StatusOK {
				t.Fatalf("replayed create() status = %d, want %d", replay.Code, http.StatusOK)
			}
			if replay.Header().Get("Idempotent-Replayed") != "true" {
				t.Fatal("replayed create() did not report the replay")
			}
			if decode(t, replay)["id"] != taskID {
				t.Fatal("replayed create() returned a second Task")
			}
		})
	}
}

func TestCreateRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			cases := map[string]struct {
				spec string
				key  string
			}{
				"invalid Service name": {spec: test.specJSON("Not A Name"), key: "key-1"},
				"no ports": {
					spec: `{"` + test.serviceField + `":"api","ports":[]}`, key: "key-1",
				},
				"duplicate ports": {
					spec: `{"` + test.serviceField + `":"api","ports":[` +
						`{"name":"a","servicePort":8080,"protocol":"tcp"},` +
						`{"name":"b","servicePort":8080,"protocol":"tcp"}]}`,
					key: "key-1",
				},
				"unsupported protocol": {
					spec: `{"` + test.serviceField + `":"api","ports":[` +
						`{"name":"a","servicePort":8080,"protocol":"sctp"}]}`,
					key: "key-1",
				},
				"missing idempotency key": {spec: test.specJSON(testService), key: ""},
			}
			for name, testCase := range cases {
				_, apiError := test.create(t, testCase.spec, testCase.key)
				if apiError == nil || apiError.Code != controlplaneapi.CodeInvalidArgument {
					t.Fatalf("create() with %s error = %#v", name, apiError)
				}
			}
		})
	}
}

func TestGetRejectsUnknownAndMalformedTasks(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, taskID := range []string{unknownTaskID, "not-a-uuid"} {
				_, apiError := test.call(t, test.endpoints.Get, http.MethodGet, taskID)
				if apiError == nil || apiError.Code != controlplaneapi.CodeNotFound {
					t.Fatalf("get(%q) error = %#v", taskID, apiError)
				}
			}
		})
	}
}

func TestListReturnsTheSessionsOwnedTasks(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			if _, apiError := test.create(t, test.specJSON("web"), "key-2"); apiError != nil {
				t.Fatal(apiError)
			}
			recorder, apiError := test.call(t, test.endpoints.List, http.MethodGet, "")
			if apiError != nil {
				t.Fatalf("list() error = %#v", apiError)
			}
			if recorder.Code != http.StatusOK {
				t.Fatalf("list() status = %d", recorder.Code)
			}
			items, _ := decode(t, recorder)["items"].([]any)
			if len(items) != 2 {
				t.Fatalf("list() returned %d items, want 2", len(items))
			}
			listed := map[string]bool{}
			for _, item := range items {
				document, _ := item.(map[string]any)
				identifier, _ := document["id"].(string)
				listed[identifier] = true
			}
			if !listed[test.taskID("key-1")] || !listed[test.taskID("key-2")] {
				t.Fatalf("list() returned %#v, want both created Tasks", listed)
			}
		})
	}
}

func TestPauseReleasesTheResourcesAndDeleteRemovesTheBinding(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")

			recorder, apiError := test.call(t, test.endpoints.Pause, http.MethodPost, taskID)
			if apiError != nil {
				t.Fatalf("pause() error = %#v", apiError)
			}
			if recorder.Code != http.StatusOK || decode(t, recorder)["id"] != taskID {
				t.Fatalf("pause() status = %d, body = %s", recorder.Code, recorder.Body.String())
			}
			if released := test.resources.counts().releases; released != 1 {
				t.Fatalf("pause() released the resources %d times, want 1", released)
			}

			deleted, apiError := test.call(t, test.endpoints.Delete, http.MethodDelete, taskID)
			if apiError != nil {
				t.Fatalf("delete() error = %#v", apiError)
			}
			if deleted.Code != http.StatusOK || decode(t, deleted)["id"] != taskID {
				t.Fatalf("delete() status = %d, body = %s", deleted.Code, deleted.Body.String())
			}
			if deletions := test.resources.counts().deletions; deletions != 1 {
				t.Fatalf("delete() removed the binding %d times, want 1", deletions)
			}
		})
	}
}

func TestResumeRejectsATaskThatIsNotPaused(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			_, apiError := test.call(t, test.endpoints.Resume, http.MethodPost, test.taskID("key-1"))
			if apiError == nil {
				t.Fatal("resume() accepted a Task that was never paused")
			}
			if apiError.Code != controlplaneapi.CodeInternal {
				t.Fatalf("resume() error code = %q, want %q", apiError.Code, controlplaneapi.CodeInternal)
			}
		})
	}
}

func TestClaimTakesRelayOwnershipExactlyOnce(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")
			request := trafficcontrol.ClaimRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
			}
			response, apiError := test.relay.Claim(t.Context(), testRelayID, request)
			if apiError != nil {
				t.Fatalf("Claim() error = %#v", apiError)
			}
			if response.Mode != test.claimMode || response.TaskID != taskID ||
				response.Service != testService || len(response.Ports) != 1 ||
				response.Ports[0].ServicePort != 8080 {
				t.Fatalf("Claim() response = %#v", response)
			}
			if _, apiError := test.relay.Claim(t.Context(), "relay-b", request); apiError == nil ||
				apiError.Code != controlplaneapi.CodeConflict {
				t.Fatalf("second Claim() error = %#v", apiError)
			}
		})
	}
}

func TestPrepareBindsTheGatewayListenersToTheTask(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")
			if _, apiError := test.relay.Claim(t.Context(), testRelayID, trafficcontrol.ClaimRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
			}); apiError != nil {
				t.Fatal(apiError)
			}
			request := trafficcontrol.PrepareRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
				RelayID: testRelayID, GatewayIP: "10.244.0.9",
				Ports: []trafficcontrol.ListenerPort{
					{Name: "http", ServicePort: 8080, ListenPort: 32001, Protocol: "tcp"},
				},
			}
			response, apiError := test.relay.Prepare(t.Context(), testRelayID, request)
			if apiError != nil {
				t.Fatalf("Prepare() error = %#v", apiError)
			}
			counts := test.resources.counts()
			if counts.captures != 1 || counts.applies != 1 {
				t.Fatalf("Prepare() mutations = %#v, want one capture and one apply", counts)
			}
			// Only Mirror hands the Gateway the original backends; the shadow
			// workload has to reach them itself (ADR 0012).
			if hasBackends := len(response.Backends) > 0; hasBackends != test.backends {
				t.Fatalf("Prepare() returned backends = %v, want %v", hasBackends, test.backends)
			}

			mismatched := request
			mismatched.Ports = []trafficcontrol.ListenerPort{
				{Name: "http", ServicePort: 9090, ListenPort: 32001, Protocol: "tcp"},
			}
			if _, apiError := test.relay.Prepare(t.Context(), testRelayID, mismatched); apiError == nil {
				t.Fatal("Prepare() accepted listener ports that do not match the Task")
			}
		})
	}
}

func TestHeartbeatRejectsAForeignRelayOwner(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")
			if _, apiError := test.relay.Claim(t.Context(), testRelayID, trafficcontrol.ClaimRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
			}); apiError != nil {
				t.Fatal(apiError)
			}
			request := trafficcontrol.HeartbeatRequest{
				Mode: test.claimMode, TaskID: taskID, RelayID: testRelayID,
			}
			if _, apiError := test.relay.Heartbeat(t.Context(), testRelayID, request); apiError != nil {
				t.Fatalf("Heartbeat() error = %#v", apiError)
			}
			if _, apiError := test.relay.Heartbeat(t.Context(), "relay-b", request); apiError == nil ||
				apiError.Code != controlplaneapi.CodeConflict {
				t.Fatalf("foreign Heartbeat() error = %#v", apiError)
			}
			unknown := trafficcontrol.HeartbeatRequest{
				Mode: test.claimMode, TaskID: unknownTaskID, RelayID: testRelayID,
			}
			if _, apiError := test.relay.Heartbeat(t.Context(), testRelayID, unknown); apiError == nil ||
				apiError.Code != controlplaneapi.CodeNotFound {
				t.Fatalf("unknown Heartbeat() error = %#v", apiError)
			}
		})
	}
}

func TestFinishReportsFailedWhenReleasingTheResourcesFails(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")
			if _, apiError := test.relay.Claim(t.Context(), testRelayID, trafficcontrol.ClaimRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
			}); apiError != nil {
				t.Fatal(apiError)
			}
			request := trafficcontrol.FinishRequest{
				Mode: test.claimMode, TaskID: taskID, RelayID: testRelayID,
			}
			response, apiError := test.relay.Finish(t.Context(), testRelayID, request)
			if apiError != nil {
				t.Fatalf("Finish() error = %#v", apiError)
			}
			if response.State != string(remotetask.Stopped) {
				t.Fatalf("Finish() state = %q, want %q", response.State, remotetask.Stopped)
			}
		})
	}
}

func TestFinishRejectsAForeignRelayOwner(t *testing.T) {
	t.Parallel()
	for _, build := range allFixtures() {
		test := build(t)
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, apiError := test.create(t, test.specJSON(testService), "key-1"); apiError != nil {
				t.Fatal(apiError)
			}
			taskID := test.taskID("key-1")
			if _, apiError := test.relay.Claim(t.Context(), testRelayID, trafficcontrol.ClaimRequest{
				Mode: test.claimMode, TaskID: taskID, Identity: ticketIdentity(),
			}); apiError != nil {
				t.Fatal(apiError)
			}
			request := trafficcontrol.FinishRequest{
				Mode: test.claimMode, TaskID: taskID, RelayID: "relay-b",
			}
			if _, apiError := test.relay.Finish(t.Context(), "relay-b", request); apiError == nil ||
				apiError.Code != controlplaneapi.CodeConflict {
				t.Fatalf("foreign Finish() error = %#v", apiError)
			}
		})
	}
}
