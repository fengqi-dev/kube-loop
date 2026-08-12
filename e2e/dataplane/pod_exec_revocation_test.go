//go:build e2e

package dataplane

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRealPodExecStopsWhenTokenFamilyIsRevoked(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	client := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatalf("ensure real Pod exec fixture: %v", err)
	}
	pods, err := client.CoreV1().Pods(harness.EchoNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=kubeloop-e2e-echo"})
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("find real Pod exec fixture: pods=%d err=%v", len(pods.Items), err)
	}
	podName := pods.Items[0].Name

	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "v2-exec-e2e.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stateStore.Close()
	now := time.Now().UTC()
	principalID, familyID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "e2e", ExternalID: "real-pod-user", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.TokenFamilies().Create(ctx, storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: "e2e-device",
		RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: now, ExpiresAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	spec, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	specJSON, err := networkspec.CanonicalJSON(spec)
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(2 * time.Minute)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: "e2e-device", ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: specJSON, NetworkSpecHash: specHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}

	provider, err := controlplanekubernetes.NewForRESTConfig(kubeRESTConfig(t), controlplanekubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := execapi.NewKubernetesExecutor(provider)
	if err != nil {
		t.Fatal(err)
	}
	activeSession := sessionapi.ActiveSession{
		ID: sessionID, Namespace: harness.EchoNamespace, ExpiresAt: expiresAt, NetworkSpecHash: specHash,
	}
	handler, err := execapi.New(
		stateStore,
		e2eExecSessionValidator{principalID: principalID, session: activeSession},
		executor,
		execapi.Config{CredentialCheckInterval: 25 * time.Millisecond},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "e2e-exec", Subjects: []string{"*"}, Namespaces: []string{harness.EchoNamespace},
		Operations: []string{"create", "stream"}, ResourceKinds: []string{"pod-exec"},
	}}})
	if err != nil {
		t.Fatal(err)
	}
	principal := controlplaneapi.Principal{
		Subject: principalID, DeviceID: "e2e-device", FamilyID: familyID, AccessExpiresAt: expiresAt,
	}
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://controlplane.e2e.invalid"}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(*http.Request) (controlplaneapi.Principal, *controlplaneapi.Error) {
			return principal, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAPIRoutes(controlplane.APIRoutes{Exec: execapi.NewRoutes(handler).Endpoints()}),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	createBody, err := json.Marshal(execapi.Spec{
		Pod: podName, Container: "echo",
		Command: []string{"python", "-u", "-c", "import time; print('v2-exec-started', flush=True); time.sleep(300)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	createURL := fmt.Sprintf("%s/kubeloop/api/sessions/%s/exec?namespace=%s", httpServer.URL, sessionID, harness.EchoNamespace)
	createRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, createURL, bytes.NewReader(createBody))
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Content-Type", "application/json")
	createRequest.Header.Set("Idempotency-Key", "real-pod-token-revocation")
	createResponse, err := http.DefaultClient.Do(createRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer createResponse.Body.Close()
	var task execapi.Document
	if err := json.NewDecoder(createResponse.Body).Decode(&task); err != nil || createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create real Pod exec Task: status=%d task=%#v err=%v", createResponse.StatusCode, task, err)
	}
	streamURL := fmt.Sprintf(
		"ws%s/kubeloop/api/sessions/%s/exec/%s/stream?namespace=%s",
		strings.TrimPrefix(httpServer.URL, "http"), sessionID, task.ID, harness.EchoNamespace,
	)
	connection, response, err := websocket.Dial(ctx, streamURL, nil)
	if err != nil {
		if response != nil {
			t.Fatalf("open real Pod exec stream: status=%d err=%v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	defer connection.CloseNow()
	waitForExecOutput(t, ctx, connection, "v2-exec-started")

	if err := stateStore.TokenFamilies().Revoke(ctx, familyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	exit := waitForCancelledExit(t, ctx, connection)
	if !exit.Cancelled || exit.Code == 0 {
		t.Fatalf("real Pod exec cancellation exit = %#v", exit)
	}
	storedTask, err := stateStore.Tasks().GetByID(ctx, task.ID)
	if err != nil || storedTask.State != "stopped" {
		t.Fatalf("real Pod exec stored Task = %#v err=%v", storedTask, err)
	}
}

type e2eExecSessionValidator struct {
	principalID string
	session     sessionapi.ActiveSession
}

func (validator e2eExecSessionValidator) RequireActive(
	_ context.Context,
	principal controlplaneapi.Principal,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if principal.Subject != validator.principalID || namespace != validator.session.Namespace || sessionID != validator.session.ID {
		return sessionapi.ActiveSession{}, &controlplaneapi.Error{Code: controlplaneapi.CodeNotFound, Message: "resource not found"}
	}
	return validator.session, nil
}

func waitForExecOutput(t *testing.T, ctx context.Context, connection *websocket.Conn, expected string) {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		_, encoded, err := connection.Read(readCtx)
		if err != nil {
			t.Fatalf("read real Pod exec output: %v", err)
		}
		frame, err := execstream.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Type == execstream.Stdout && strings.Contains(string(frame.Payload), expected) {
			return
		}
		if frame.Type == execstream.Exit {
			exit, _ := execstream.DecodeExit(frame)
			t.Fatalf("real Pod exec exited before revocation: %#v", exit)
		}
	}
}

func waitForCancelledExit(t *testing.T, ctx context.Context, connection *websocket.Conn) execstream.ExitStatus {
	t.Helper()
	readCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		_, encoded, err := connection.Read(readCtx)
		if err != nil {
			t.Fatalf("read real Pod exec cancellation: %v", err)
		}
		frame, err := execstream.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		if frame.Type != execstream.Exit {
			continue
		}
		exit, err := execstream.DecodeExit(frame)
		if err != nil {
			t.Fatal(err)
		}
		return exit
	}
}
