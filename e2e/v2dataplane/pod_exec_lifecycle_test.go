//go:build e2e

package v2dataplane

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientexec "github.com/fengqi-dev/kube-loop/internal/clientv2/exec"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/execapi"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const execLifecycleAccessToken = "v2-e2e-exec-lifecycle"

type runningExecController struct {
	server   *controller.Server
	listener net.Listener
	done     chan error
}

func startExecController(
	t *testing.T,
	address string,
	stateStore *storage.Store,
	executor execapi.Executor,
	principal controller.Principal,
	session sessionapi.ActiveSession,
) *runningExecController {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen for exec Controller: %v", err)
	}
	handler, err := execapi.New(
		stateStore,
		e2eExecSessionValidator{principalID: principal.Subject, session: session},
		executor,
		execapi.Config{CredentialCheckInterval: 25 * time.Millisecond},
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	router := controller.NewAPIRouter()
	if err := router.Handle(http.MethodPost, "/api/v2/sessions/{sessionID}/exec", handler); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	if err := router.Handle(http.MethodGet, "/api/v2/sessions/{sessionID}/exec/{taskID}/stream", handler); err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{{
		ID: "v2-e2e-exec-lifecycle", Subjects: []string{principal.Subject}, Namespaces: []string{session.Namespace},
		Operations: []string{"create", "stream"}, ResourceKinds: []string{"pod-exec"},
	}}})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	server, err := controller.NewServer(
		controller.Config{PublicURL: "http://" + listener.Addr().String()}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(request *http.Request) (controller.Principal, *controller.APIError) {
			if request.Header.Get("Authorization") != "Bearer "+execLifecycleAccessToken {
				return controller.Principal{}, &controller.APIError{Code: controller.CodeUnauthenticated, Message: "invalid e2e access token"}
			}
			return principal, nil
		})),
		controller.WithAuthorizer(policy), controller.WithAPIHandler(router),
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	running := &runningExecController{server: server, listener: listener, done: make(chan error, 1)}
	go func() { running.done <- server.Serve(listener) }()
	return running
}

func (running *runningExecController) Address() string { return running.listener.Addr().String() }

func (running *runningExecController) Stop(t *testing.T) {
	t.Helper()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := running.server.Shutdown(shutdownContext); err != nil {
		t.Fatalf("shutdown exec Controller: %v", err)
	}
	select {
	case err := <-running.done:
		if err != nil {
			t.Fatalf("serve exec Controller: %v", err)
		}
	case <-shutdownContext.Done():
		t.Fatal("exec Controller serve loop did not stop")
	}
}

func TestRealPodExecTTYDisconnectAndControllerRestart(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real Pod exec fixture: %v", err)
	}
	pods, err := kubeClient.CoreV1().Pods(harness.EchoNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=kubeloop-e2e-echo"})
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("find real Pod exec fixture: pods=%d err=%v", len(pods.Items), err)
	}
	podName := pods.Items[0].Name

	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "v2-exec-lifecycle.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, familyID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "v2-e2e-exec-device"
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "v2-e2e", ExternalID: "exec-lifecycle", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.TokenFamilies().Create(ctx, storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: deviceID,
		RefreshTokenHash: bytes.Repeat([]byte{9}, 32), CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(5 * time.Minute)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := controllerkubernetes.NewForRESTConfig(kubeRESTConfig(t), controllerkubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := execapi.NewKubernetesExecutor(provider)
	if err != nil {
		t.Fatal(err)
	}
	principal := controller.Principal{
		Subject: principalID, DeviceID: deviceID, FamilyID: familyID, AccessExpiresAt: expiresAt,
	}
	activeSession := sessionapi.ActiveSession{
		ID: sessionID, Namespace: harness.EchoNamespace, Generation: 1,
		ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}
	running := startExecController(t, "127.0.0.1:0", stateStore, executor, principal, activeSession)
	controllerAddress := running.Address()
	controllerStopped := false
	t.Cleanup(func() {
		if !controllerStopped {
			running.Stop(t)
		}
	})
	serverProfile := profile.Profile{ID: "v2-e2e-exec", BaseURL: "http://" + controllerAddress}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: execLifecycleAccessToken, AccessExpiresAt: now.Add(5 * time.Minute),
			RefreshToken: "unused", RefreshExpiresAt: now.Add(5 * time.Minute), DeviceID: deviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{})
	if err != nil {
		t.Fatal(err)
	}
	remoteSession := remote.Session{
		ID: sessionID, Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
		NetworkSpec: network, NetworkSpecHash: networkHash,
	}

	ttyStream, err := clientexec.Start(ctx, remoteClient, serverProfile, remoteSession, remote.ExecSpec{
		Pod: podName, Container: "echo", TTY: true,
		Command: []string{"python", "-u", "-c", "import os,sys; value=sys.stdin.readline().strip(); size=os.get_terminal_size(0); print(f'{size.lines} {size.columns}'); print(value)"},
	})
	if err != nil {
		t.Fatalf("start real TTY exec: %v", err)
	}
	if err := ttyStream.Resize(ctx, 120, 40); err != nil {
		t.Fatalf("resize real TTY exec: %v", err)
	}
	if err := ttyStream.WriteStdin(ctx, []byte("resize-ok\n")); err != nil {
		t.Fatalf("write real TTY exec stdin: %v", err)
	}
	output, exit := readExecToExit(t, ctx, ttyStream)
	if exit.Code != 0 || exit.Cancelled || !strings.Contains(output, "40 120") || !strings.Contains(output, "resize-ok") {
		t.Fatalf("real TTY output=%q exit=%#v", output, exit)
	}

	abruptTask, err := remoteClient.CreateExecTask(ctx, serverProfile, remoteSession, remote.ExecSpec{
		Pod: podName, Container: "echo",
		Command: []string{"python", "-u", "-c", "import time; print('disconnect-started', flush=True); time.sleep(300)"},
	}, "pod-exec:"+uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	abruptConnection, err := remoteClient.OpenExecStream(ctx, serverProfile, remoteSession, abruptTask)
	if err != nil {
		t.Fatal(err)
	}
	waitForExecOutput(t, ctx, abruptConnection, "disconnect-started")
	abruptConnection.CloseNow()
	abruptStored := waitForExecTaskState(t, ctx, stateStore, abruptTask.ID, "stopped")
	assertCancelledTaskResult(t, abruptStored)

	restartStream, err := clientexec.Start(ctx, remoteClient, serverProfile, remoteSession, remote.ExecSpec{
		Pod: podName, Container: "echo",
		Command: []string{"python", "-u", "-c", "import time; print('restart-started', flush=True); time.sleep(300)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForClientExecOutput(t, ctx, restartStream, "restart-started")
	running.Stop(t)
	controllerStopped = true
	assertCancelledTaskResult(t, waitForExecTaskState(t, ctx, stateStore, restartStream.Task().ID, "stopped"))
	_ = restartStream.Close()

	running = startExecController(t, controllerAddress, stateStore, executor, principal, activeSession)
	controllerStopped = false
	afterRestart, err := clientexec.Start(ctx, remoteClient, serverProfile, remoteSession, remote.ExecSpec{
		Pod: podName, Container: "echo", Command: []string{"printf", "after-restart"},
	})
	if err != nil {
		t.Fatalf("start Pod exec after Controller restart: %v", err)
	}
	afterOutput, afterExit := readExecToExit(t, ctx, afterRestart)
	if afterExit.Code != 0 || afterExit.Cancelled || !strings.Contains(afterOutput, "after-restart") {
		t.Fatalf("post-restart output=%q exit=%#v", afterOutput, afterExit)
	}
}

func readExecToExit(t *testing.T, ctx context.Context, stream *clientexec.Stream) (string, execstream.ExitStatus) {
	t.Helper()
	readContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var output strings.Builder
	for {
		frame, err := stream.Read(readContext)
		if err != nil {
			t.Fatalf("read Pod exec stream: %v", err)
		}
		switch frame.Type {
		case execstream.Stdout, execstream.Stderr:
			output.Write(frame.Payload)
		case execstream.Exit:
			exit, err := execstream.DecodeExit(frame)
			if err != nil {
				t.Fatal(err)
			}
			return output.String(), exit
		}
	}
}

func waitForClientExecOutput(t *testing.T, ctx context.Context, stream *clientexec.Stream, expected string) {
	t.Helper()
	readContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		frame, err := stream.Read(readContext)
		if err != nil {
			t.Fatalf("read Pod exec output: %v", err)
		}
		if frame.Type == execstream.Stdout && strings.Contains(string(frame.Payload), expected) {
			return
		}
		if frame.Type == execstream.Exit {
			exit, _ := execstream.DecodeExit(frame)
			t.Fatalf("Pod exec exited before %q: %#v", expected, exit)
		}
	}
}

func waitForExecTaskState(
	t *testing.T,
	ctx context.Context,
	stateStore *storage.Store,
	taskID string,
	want string,
) storage.Task {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		task, err := stateStore.Tasks().GetByID(ctx, taskID)
		if err == nil && string(task.State) == want {
			return task
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("Pod exec Task %s did not reach %s: task=%#v err=%v", taskID, want, task, err)
		case <-ticker.C:
		}
	}
}

func assertCancelledTaskResult(t *testing.T, task storage.Task) {
	t.Helper()
	var result struct {
		ExitCode  uint32 `json:"exitCode"`
		Cancelled bool   `json:"cancelled"`
	}
	if err := json.Unmarshal(task.Result, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Cancelled || result.ExitCode == 0 {
		t.Fatalf("cancelled Pod exec result = %#v raw=%s", result, task.Result)
	}
}
