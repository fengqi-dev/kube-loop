package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
)

func TestLoadAuthStatus(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	model := Model{
		activeProfile: clientprofile.Profile{ID: "server"},
		state: &State{credentials: &appCommandCredentialStore{credential: credentials.Credential{
			AccessToken: "access", RefreshToken: "refresh", UserName: "operator",
			AccessExpiresAt: now.Add(time.Hour), RefreshExpiresAt: now.Add(24 * time.Hour),
		}}},
	}
	message, ok := loadAuthStatus(model)().(authStatusMsg)
	if !ok || message.err != nil || !message.session.Authenticated || message.session.UserName != "operator" {
		t.Fatalf("loadAuthStatus() message = %#v", message)
	}
}

func TestLoadRemoteInventoryAcrossNamespaces(t *testing.T) {
	t.Parallel()

	model, closeServer := newAppCommandRemoteModel(t)
	defer closeServer()

	namespaces, ok := loadNamespaces(model)().(namespacesLoadedMsg)
	if !ok || namespaces.err != nil || len(namespaces.namespaces) != 2 {
		t.Fatalf("loadNamespaces() message = %#v", namespaces)
	}
	pods, ok := loadPods(model)().(podsLoadedMsg)
	if !ok || pods.err != nil || len(pods.pods) != 2 || pods.pods[0].Namespace != "alpha" {
		t.Fatalf("loadPods() message = %#v", pods)
	}
	services, ok := loadServices(model)().(servicesLoadedMsg)
	if !ok || services.err != nil || len(services.services) != 2 || services.services[1].Namespace != "beta" {
		t.Fatalf("loadServices() message = %#v", services)
	}
}

func TestLoadAcrossNamespacesErrorContract(t *testing.T) {
	t.Parallel()

	model, closeServer := newAppCommandRemoteModel(t)
	defer closeServer()
	want := errors.New("alpha unavailable")
	items, err := loadAcrossNamespaces(model, func(namespace string) ([]string, error) {
		if namespace == "alpha" {
			return nil, want
		}
		return []string{namespace}, nil
	})
	if err != nil || len(items) != 1 || items[0] != "beta" {
		t.Fatalf("partial inventory = %#v, error = %v", items, err)
	}
	items, err = loadAcrossNamespaces(model, func(string) ([]string, error) {
		return nil, want
	})
	if !errors.Is(err, want) || len(items) != 0 {
		t.Fatalf("failed inventory = %#v, error = %v", items, err)
	}
}

func TestWaitExecEvent(t *testing.T) {
	t.Parallel()

	t.Run("event", func(t *testing.T) {
		t.Parallel()

		state := &State{ctx: t.Context(), execEvents: make(chan clientexec.Event, 1)}
		state.execEvents <- clientexec.Event{TaskID: "exec-1", Type: clientexec.EventExit, ExitCode: 7}
		message, ok := waitExecEvent(state)().(execEventMsg)
		if !ok || message.event.TaskID != "exec-1" || message.event.ExitCode != 7 {
			t.Fatalf("waitExecEvent() message = %#v", message)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		state := &State{ctx: ctx, execEvents: make(chan clientexec.Event)}
		if message := waitExecEvent(state)(); message != nil {
			t.Fatalf("cancelled waitExecEvent() = %#v", message)
		}
	})
}

func TestApplyExecEvent(t *testing.T) {
	t.Parallel()

	model := Model{}
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 9000)))
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventStdout, Data: encoded})
	if len(model.execTasks) != 1 || model.execTasks[0].State != taskStateRunning ||
		len(model.execTasks[0].Output) != 8192 {
		t.Fatalf("stdout event tasks = %#v", model.execTasks)
	}
	output := model.execTasks[0].Output
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventStderr, Data: "invalid"})
	if model.execTasks[0].Output != output {
		t.Fatal("invalid base64 changed exec output")
	}
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventExit, ExitCode: 23})
	if model.execTasks[0].State != "exit 23" {
		t.Fatalf("exit state = %q", model.execTasks[0].State)
	}
	model.applyExecEvent(clientexec.Event{TaskID: "exec-1", Type: clientexec.EventError, Error: "stream failed"})
	if model.execTasks[0].State != "error" || !strings.HasSuffix(model.execTasks[0].Output, "stream failed") {
		t.Fatalf("error event task = %#v", model.execTasks[0])
	}
}

func TestUpsertExecTaskPreservesOutput(t *testing.T) {
	t.Parallel()

	items := []execTaskView{{ID: "exec-1", State: taskStateRunning, Output: "existing"}}
	items = upsertExecTask(items, execTaskView{ID: "exec-1", State: "completed"})
	if len(items) != 1 || items[0].State != "completed" || items[0].Output != "existing" {
		t.Fatalf("updated tasks = %#v", items)
	}
	items = upsertExecTask(items, execTaskView{ID: "exec-2", State: taskStateRunning})
	if len(items) != 2 || items[1].ID != "exec-2" {
		t.Fatalf("appended tasks = %#v", items)
	}
}

type appCommandCredentialStore struct {
	credential credentials.Credential
}

func (store *appCommandCredentialStore) Set(_ string, credential credentials.Credential) error {
	store.credential = credential
	return nil
}

func (store *appCommandCredentialStore) Get(string) (credentials.Credential, error) {
	if store.credential.AccessToken == "" {
		return credentials.Credential{}, credentials.ErrNotFound
	}
	return store.credential, nil
}

func (store *appCommandCredentialStore) Delete(string) error {
	store.credential = credentials.Credential{}
	return nil
}

type appCommandTokenRefresher struct{}

func (appCommandTokenRefresher) Refresh(
	context.Context,
	string,
	credentials.Credential,
) (credentials.Credential, error) {
	return credentials.Credential{}, errors.New("unexpected token refresh")
}

func newAppCommandRemoteModel(t *testing.T) (Model, func()) {
	t.Helper()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		var items any
		switch request.URL.Path {
		case "/api/namespaces":
			items = []clientremote.Namespace{{Name: "alpha"}, {Name: "beta"}}
		case "/api/namespaces/alpha/pods":
			items = []clientremote.Pod{{Name: "api", Namespace: "alpha"}}
		case "/api/namespaces/beta/pods":
			items = []clientremote.Pod{{Name: "worker", Namespace: "beta"}}
		case "/api/namespaces/alpha/services":
			items = []clientremote.Service{{Name: "api", Namespace: "alpha"}}
		case "/api/namespaces/beta/services":
			items = []clientremote.Service{{Name: "worker", Namespace: "beta"}}
		default:
			http.NotFound(writer, request)
			return
		}
		if err := json.NewEncoder(writer).Encode(map[string]any{"items": items}); err != nil {
			t.Error(err)
		}
	}))
	store := &appCommandCredentialStore{credential: credentials.Credential{
		AccessToken: "access", RefreshToken: "refresh",
		AccessExpiresAt: time.Now().Add(time.Hour), RefreshExpiresAt: time.Now().Add(24 * time.Hour),
	}}
	remote, err := clientremote.New(
		store,
		appCommandTokenRefresher{},
		clientremote.Config{HTTPClient: server.Client()},
	)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	profile := clientprofile.Profile{ID: "server", BaseURL: server.URL}
	return Model{
		activeProfile: profile,
		authSession:   AuthSession{Authenticated: true},
		state:         &State{ctx: t.Context(), remote: remote, credentials: store},
	}, server.Close
}
