//go:build e2e

package v2dataplane

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/clientv2/filetransfer"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controller/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/fileopsapi"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
	"github.com/kballard/go-shellquote"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fileE2EIdentity struct {
	principal controller.Principal
	active    sessionapi.ActiveSession
	session   remote.Session
	familyID  string
	token     string
}

type fileUploadResult struct {
	task   remote.FileTransferTask
	result filestream.TransferResult
	err    error
}

type delayedReadSeeker struct {
	*bytes.Reader
	delay time.Duration
	chunk int
}

func (reader *delayedReadSeeker) Read(value []byte) (int, error) {
	if len(value) > reader.chunk {
		value = value[:reader.chunk]
	}
	time.Sleep(reader.delay)
	return reader.Reader.Read(value)
}

func startFileController(
	t *testing.T,
	address string,
	stateStore *storage.Store,
	targets fileapi.TargetResolver,
	transfers fileapi.TransferExecutor,
	fileOperations fileopsapi.Operator,
	identity fileE2EIdentity,
) *runningExecController {
	t.Helper()
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen for file Controller: %v", err)
	}
	handler, err := fileapi.New(
		stateStore,
		e2eExecSessionValidator{principalID: identity.principal.Subject, session: identity.active},
		targets,
		transfers,
		fileapi.Config{
			MaximumBytes: 64 << 20, AllowedPathRoots: []string{"/tmp"},
			CredentialCheckInterval: 25 * time.Millisecond,
		},
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	operations, err := fileopsapi.New(
		stateStore,
		e2eExecSessionValidator{principalID: identity.principal.Subject, session: identity.active},
		targets,
		fileOperations,
		fileopsapi.Config{AllowedPathRoots: []string{"/tmp"}},
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	router := controller.NewAPIRouter()
	for _, route := range []struct{ method, pattern string }{
		{http.MethodPost, "/api/v2/sessions/{sessionID}/file-transfers"},
		{http.MethodGet, "/api/v2/sessions/{sessionID}/file-transfers/{taskID}"},
		{http.MethodGet, "/api/v2/sessions/{sessionID}/file-transfers/{taskID}/stream"},
	} {
		if err := router.Handle(route.method, route.pattern, handler); err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
	}
	for _, route := range []struct{ method, pattern string }{
		{http.MethodPost, "/api/v2/sessions/{sessionID}/pod-files/list"},
		{http.MethodPost, "/api/v2/sessions/{sessionID}/pod-files/create"},
		{http.MethodPost, "/api/v2/sessions/{sessionID}/pod-files/rename"},
		{http.MethodPost, "/api/v2/sessions/{sessionID}/pod-files/delete"},
		{http.MethodGet, "/api/v2/sessions/{sessionID}/pod-files/operations/{taskID}"},
	} {
		if err := router.Handle(route.method, route.pattern, operations); err != nil {
			_ = listener.Close()
			t.Fatal(err)
		}
	}
	policy, err := authorization.New(authorization.Policy{Rules: []authorization.Rule{
		{
			ID: "v2-e2e-file-transfer", Subjects: []string{identity.principal.Subject},
			Namespaces: []string{identity.session.Namespace}, Operations: []string{"create", "get", "stream"},
			ResourceKinds: []string{"file-transfers"},
		},
		{
			ID: "v2-e2e-file-operations", Subjects: []string{identity.principal.Subject},
			Namespaces: []string{identity.session.Namespace}, Operations: []string{"list", "create", "update", "delete", "get"},
			ResourceKinds: []string{"pod-files"},
		},
	}})
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	server, err := controller.NewServer(
		controller.Config{PublicURL: "http://" + listener.Addr().String()}, controller.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controller.WithAuthenticator(controller.AuthenticatorFunc(func(request *http.Request) (controller.Principal, *controller.APIError) {
			if request.Header.Get("Authorization") != "Bearer "+identity.token {
				return controller.Principal{}, &controller.APIError{Code: controller.CodeUnauthenticated, Message: "invalid file E2E token"}
			}
			return identity.principal, nil
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

func createFileIdentity(
	t *testing.T,
	ctx context.Context,
	stateStore *storage.Store,
	principalID, deviceID, namespace string,
	network networkspec.Spec,
	networkHash, token string,
	hashByte byte,
) fileE2EIdentity {
	t.Helper()
	now := time.Now().UTC()
	familyID, sessionID := uuid.NewString(), uuid.NewString()
	expiresAt := now.Add(5 * time.Minute)
	if err := stateStore.TokenFamilies().Create(ctx, storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: deviceID,
		RefreshTokenHash: bytes.Repeat([]byte{hashByte}, 32), CreatedAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: namespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return fileE2EIdentity{
		principal: controller.Principal{
			Subject: principalID, DeviceID: deviceID, FamilyID: familyID, AccessExpiresAt: expiresAt,
		},
		active: sessionapi.ActiveSession{
			ID: sessionID, Namespace: namespace, Generation: 1,
			ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
		},
		session: remote.Session{
			ID: sessionID, Namespace: namespace, State: "active", Generation: 1,
			CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
			NetworkSpec: network, NetworkSpecHash: networkHash,
		},
		familyID: familyID,
		token:    token,
	}
}

func TestRealFileTransferRevocationControllerRestartAndResume(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real file transfer fixture: %v", err)
	}
	pods, err := kubeClient.CoreV1().Pods(harness.EchoNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=kubeloop-e2e-echo"})
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("find real file transfer fixture: pods=%d err=%v", len(pods.Items), err)
	}
	podName := pods.Items[0].Name

	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "v2-file-lifecycle.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, deviceID := uuid.NewString(), "v2-e2e-file-device"
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "v2-e2e", ExternalID: "file-lifecycle", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := controllerkubernetes.NewForRESTConfig(kubeRESTConfig(t), controllerkubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := fileapi.NewKubernetesTargetResolver(provider)
	if err != nil {
		t.Fatal(err)
	}
	podExecutor, err := execapi.NewKubernetesExecutor(provider)
	if err != nil {
		t.Fatal(err)
	}
	transfers, err := fileapi.NewKubernetesTransferExecutor(podExecutor, 64<<20)
	if err != nil {
		t.Fatal(err)
	}
	fileOperations, err := fileopsapi.NewKubernetesOperator(podExecutor)
	if err != nil {
		t.Fatal(err)
	}
	firstIdentity := createFileIdentity(
		t, ctx, stateStore, principalID, deviceID, harness.EchoNamespace,
		network, networkHash, "v2-e2e-file-revoked", 5,
	)
	running := startFileController(t, "127.0.0.1:0", stateStore, targets, transfers, fileOperations, firstIdentity)
	controllerAddress := running.Address()
	controllerStopped := false
	t.Cleanup(func() {
		if !controllerStopped {
			running.Stop(t)
		}
	})
	serverProfile := profile.Profile{ID: "v2-e2e-file", BaseURL: "http://" + controllerAddress}
	firstClient := newFileRemoteClient(t, serverProfile.ID, firstIdentity)
	payload := bytes.Repeat([]byte("KubeLoop-V2-file-transfer\n"), (8<<20)/len("KubeLoop-V2-file-transfer\n"))
	checksum := sha256.Sum256(payload)
	testID := uuid.NewString()
	revokedPath := "/tmp/kubeloop-v2-revoked-" + testID
	restartPath := "/tmp/kubeloop-v2-restart-" + testID
	revokedResumeID, restartResumeID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		command := "rm -f -- " + strings.Join([]string{
			revokedPath, revokedPath + ".kubeloop-" + revokedResumeID + ".part",
			restartPath, restartPath + ".kubeloop-" + restartResumeID + ".part",
		}, " ")
		_ = podExecutor.Exec(cleanupContext, firstIdentity.principal, harness.EchoNamespace, execapi.Spec{
			Pod: podName, Container: "echo", Command: []string{"/bin/sh", "-c", command},
		}, execapi.Streams{Stdout: io.Discard, Stderr: io.Discard})
	})

	firstProgress := make(chan filestream.ProgressStatus, 8)
	firstUpload := make(chan fileUploadResult, 1)
	go func() {
		task, result, uploadErr := clientfiletransfer.Upload(
			ctx, firstClient, serverProfile, firstIdentity.session,
			fileSpec("upload", podName, revokedPath, payload, checksum, revokedResumeID),
			&delayedReadSeeker{Reader: bytes.NewReader(payload), delay: 10 * time.Millisecond, chunk: 16 << 10},
			func(progress filestream.ProgressStatus) { sendFileProgress(firstProgress, progress) },
		)
		firstUpload <- fileUploadResult{task: task, result: result, err: uploadErr}
	}()
	waitForFileProgress(t, ctx, firstProgress)
	revokedAt := time.Now()
	if err := stateStore.TokenFamilies().Revoke(ctx, firstIdentity.familyID, revokedAt.UTC()); err != nil {
		t.Fatal(err)
	}
	firstResult := waitForFileUpload(t, ctx, firstUpload)
	if firstResult.err == nil || firstResult.task.ID == "" || time.Since(revokedAt) > 2*time.Second {
		t.Fatalf("revoked real upload did not stop promptly: task=%#v result=%#v err=%v elapsed=%s", firstResult.task, firstResult.result, firstResult.err, time.Since(revokedAt))
	}
	assertCancelledFileTask(t, waitForExecTaskState(t, ctx, stateStore, firstResult.task.ID, "stopped"))

	running.Stop(t)
	controllerStopped = true
	secondIdentity := createFileIdentity(
		t, ctx, stateStore, principalID, deviceID, harness.EchoNamespace,
		network, networkHash, "v2-e2e-file-active", 6,
	)
	running = startFileController(t, controllerAddress, stateStore, targets, transfers, fileOperations, secondIdentity)
	controllerStopped = false
	secondClient := newFileRemoteClient(t, serverProfile.ID, secondIdentity)

	restartProgress := make(chan filestream.ProgressStatus, 8)
	restartUpload := make(chan fileUploadResult, 1)
	go func() {
		task, result, uploadErr := clientfiletransfer.Upload(
			ctx, secondClient, serverProfile, secondIdentity.session,
			fileSpec("upload", podName, restartPath, payload, checksum, restartResumeID),
			&delayedReadSeeker{Reader: bytes.NewReader(payload), delay: 10 * time.Millisecond, chunk: 16 << 10},
			func(progress filestream.ProgressStatus) { sendFileProgress(restartProgress, progress) },
		)
		restartUpload <- fileUploadResult{task: task, result: result, err: uploadErr}
	}()
	waitForFileProgress(t, ctx, restartProgress)
	running.Stop(t)
	controllerStopped = true
	restartResult := waitForFileUpload(t, ctx, restartUpload)
	if restartResult.err == nil || restartResult.task.ID == "" {
		t.Fatalf("upload unexpectedly survived Controller restart: task=%#v result=%#v err=%v", restartResult.task, restartResult.result, restartResult.err)
	}
	assertCancelledFileTask(t, waitForExecTaskState(t, ctx, stateStore, restartResult.task.ID, "stopped"))

	running = startFileController(t, controllerAddress, stateStore, targets, transfers, fileOperations, secondIdentity)
	controllerStopped = false
	completedTask, completedResult, err := clientfiletransfer.Upload(
		ctx, secondClient, serverProfile, secondIdentity.session,
		fileSpec("upload", podName, restartPath, payload, checksum, restartResumeID),
		bytes.NewReader(payload), nil,
	)
	if err != nil || completedResult.Status != filestream.ResultSucceeded ||
		completedTask.Offset == 0 || completedTask.Offset >= uint64(len(payload)) {
		t.Fatalf("resume real upload after Controller restart: task=%#v result=%#v err=%v", completedTask, completedResult, err)
	}
	var downloaded bytes.Buffer
	_, downloadResult, err := clientfiletransfer.Download(
		ctx, secondClient, serverProfile, secondIdentity.session,
		remote.FileTransferSpec{
			Direction: "download", Kind: "file", Pod: podName, Container: "echo", RemotePath: restartPath,
		},
		&downloaded, nil,
	)
	if err != nil || downloadResult.Status != filestream.ResultSucceeded || !bytes.Equal(downloaded.Bytes(), payload) {
		t.Fatalf("download resumed real Pod file: bytes=%d result=%#v err=%v", downloaded.Len(), downloadResult, err)
	}

	// Remote file operations must preserve shell-significant path characters
	// without letting them change the command, then directory transfer must
	// preserve nested files and empty directories in both directions.
	specialRoot := "/tmp/kubeloop-v2-special-" + testID
	remoteFile := specialRoot + "/file ' $;.txt"
	renamedFile := specialRoot + "/renamed ' $;.txt"
	remoteDirectory := specialRoot + "/directory ' $;"
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = podExecutor.Exec(cleanupContext, secondIdentity.principal, harness.EchoNamespace, execapi.Spec{
			Pod: podName, Container: "echo",
			Command: []string{"/bin/sh", "-c", "rm -rf -- " + shellquote.Join(specialRoot)},
		}, execapi.Streams{Stdout: io.Discard, Stderr: io.Discard})
	})
	fileOperation, fileOperationErr := secondClient.CreatePodFileOperation(
		ctx, serverProfile, secondIdentity.session, "create",
		remote.PodFileSpec{Pod: podName, Container: "echo", Path: specialRoot, Kind: "directory"},
		"file-create-root:"+uuid.NewString(),
	)
	assertPodFileOperationSucceeded(t, fileOperation, fileOperationErr)
	fileOperation, fileOperationErr = secondClient.CreatePodFileOperation(
		ctx, serverProfile, secondIdentity.session, "create",
		remote.PodFileSpec{Pod: podName, Container: "echo", Path: remoteFile, Kind: "file"},
		"file-create-special:"+uuid.NewString(),
	)
	assertPodFileOperationSucceeded(t, fileOperation, fileOperationErr)
	fileOperation, fileOperationErr = secondClient.CreatePodFileOperation(
		ctx, serverProfile, secondIdentity.session, "rename",
		remote.PodFileSpec{Pod: podName, Container: "echo", Path: remoteFile, Destination: renamedFile},
		"file-rename-special:"+uuid.NewString(),
	)
	assertPodFileOperationSucceeded(t, fileOperation, fileOperationErr)
	listing, err := secondClient.ListPodFiles(ctx, serverProfile, secondIdentity.session, remote.PodFileSpec{
		Pod: podName, Container: "echo", Path: specialRoot,
	})
	if err != nil || !containsPodFile(listing.Items, "renamed ' $;.txt", "file") {
		t.Fatalf("list special remote path: listing=%#v err=%v", listing, err)
	}
	fileOperation, fileOperationErr = secondClient.CreatePodFileOperation(
		ctx, serverProfile, secondIdentity.session, "delete",
		remote.PodFileSpec{Pod: podName, Container: "echo", Path: renamedFile},
		"file-delete-special:"+uuid.NewString(),
	)
	assertPodFileOperationSucceeded(t, fileOperation, fileOperationErr)

	localDirectory := filepath.Join(t.TempDir(), "directory ' $;")
	if err := os.MkdirAll(filepath.Join(localDirectory, "nested ' $;"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(localDirectory, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	directoryContents := []byte("KubeLoop V2 directory transfer\n")
	if err := os.WriteFile(filepath.Join(localDirectory, "nested ' $;", "payload.txt"), directoryContents, 0o640); err != nil {
		t.Fatal(err)
	}
	managerRoot := t.TempDir()
	transferManager, err := clientfiletransfer.NewManager(secondClient, clientfiletransfer.Config{
		StatePath: filepath.Join(managerRoot, "transfers.json"), TemporaryDir: filepath.Join(managerRoot, "temporary"),
		MaximumBytes: 64 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transferManager.Shutdown() })
	uploadTask, err := transferManager.Start(serverProfile, secondIdentity.session, clientfiletransfer.Request{
		ProfileID: serverProfile.ID, Direction: "upload", Kind: "directory",
		Pod: podName, Container: "echo", LocalPath: localDirectory, RemotePath: remoteDirectory, Overwrite: true,
	})
	if err != nil {
		t.Fatalf("start real directory upload: %v", err)
	}
	waitForManagedFileTask(t, ctx, transferManager, uploadTask.ID, clientfiletransfer.StatusCompleted)
	downloadDirectory := filepath.Join(t.TempDir(), "download ' $;")
	downloadTask, err := transferManager.Start(serverProfile, secondIdentity.session, clientfiletransfer.Request{
		ProfileID: serverProfile.ID, Direction: "download", Kind: "directory",
		Pod: podName, Container: "echo", LocalPath: downloadDirectory, RemotePath: remoteDirectory,
	})
	if err != nil {
		t.Fatalf("start real directory download: %v", err)
	}
	waitForManagedFileTask(t, ctx, transferManager, downloadTask.ID, clientfiletransfer.StatusCompleted)
	downloadedDirectoryContents, err := os.ReadFile(filepath.Join(downloadDirectory, "nested ' $;", "payload.txt"))
	if err != nil || !bytes.Equal(downloadedDirectoryContents, directoryContents) {
		t.Fatalf("downloaded directory payload=%q err=%v", downloadedDirectoryContents, err)
	}
	emptyInfo, err := os.Stat(filepath.Join(downloadDirectory, "empty"))
	if err != nil || !emptyInfo.IsDir() {
		t.Fatalf("downloaded empty directory=%#v err=%v", emptyInfo, err)
	}
	fileOperation, fileOperationErr = secondClient.CreatePodFileOperation(
		ctx, serverProfile, secondIdentity.session, "delete",
		remote.PodFileSpec{Pod: podName, Container: "echo", Path: specialRoot, Recursive: true},
		"file-delete-tree:"+uuid.NewString(),
	)
	assertPodFileOperationSucceeded(t, fileOperation, fileOperationErr)
}

func assertPodFileOperationSucceeded(t *testing.T, task remote.PodFileTask, err error) {
	t.Helper()
	if err != nil || task.State != "stopped" || !task.Result.Completed || task.Result.Error != "" {
		t.Fatalf("remote Pod file operation: task=%#v err=%v", task, err)
	}
}

func containsPodFile(items []remote.PodFileEntry, name, kind string) bool {
	for _, item := range items {
		if item.Name == name && item.Kind == kind {
			return true
		}
	}
	return false
}

func waitForManagedFileTask(
	t *testing.T,
	ctx context.Context,
	manager *clientfiletransfer.Manager,
	taskID, want string,
) clientfiletransfer.Task {
	t.Helper()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		for _, task := range manager.List("") {
			if task.ID != taskID {
				continue
			}
			if task.Status == want {
				return task
			}
			if task.Status == clientfiletransfer.StatusFailed || task.Status == clientfiletransfer.StatusCancelled ||
				task.Status == clientfiletransfer.StatusInterrupted {
				t.Fatalf("managed file Task %s ended as %s: %s", taskID, task.Status, task.Error)
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-deadline.C:
			t.Fatalf("managed file Task %s did not reach %s", taskID, want)
		case <-ticker.C:
		}
	}
}

func newFileRemoteClient(t *testing.T, profileID string, identity fileE2EIdentity) *remote.Client {
	t.Helper()
	store := &e2eCredentialStore{
		profileID: profileID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: identity.token, AccessExpiresAt: identity.principal.AccessExpiresAt,
			RefreshToken: "unused", RefreshExpiresAt: identity.principal.AccessExpiresAt,
			DeviceID: identity.principal.DeviceID,
		},
	}
	client, err := remote.New(store, e2eTokenRefresher{}, remote.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func fileSpec(
	direction, pod, remotePath string,
	payload []byte,
	checksum [32]byte,
	resumeID string,
) remote.FileTransferSpec {
	return remote.FileTransferSpec{
		Direction: direction, Kind: "file", Pod: pod, Container: "echo", RemotePath: remotePath,
		Size: uint64(len(payload)), Checksum: filestream.FormatChecksum(checksum), Overwrite: true, ResumeID: resumeID,
	}
}

func sendFileProgress(channel chan<- filestream.ProgressStatus, progress filestream.ProgressStatus) {
	select {
	case channel <- progress:
	default:
	}
}

func waitForFileProgress(t *testing.T, ctx context.Context, progress <-chan filestream.ProgressStatus) {
	t.Helper()
	for {
		select {
		case value := <-progress:
			if value.Transferred > 0 {
				return
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(30 * time.Second):
			t.Fatal("timed out waiting for real file transfer progress")
		}
	}
}

func waitForFileUpload(t *testing.T, ctx context.Context, result <-chan fileUploadResult) fileUploadResult {
	t.Helper()
	select {
	case value := <-result:
		return value
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for interrupted file upload")
	}
	return fileUploadResult{}
}

func assertCancelledFileTask(t *testing.T, task storage.Task) {
	t.Helper()
	var result struct {
		Cancelled bool `json:"cancelled"`
	}
	if err := json.Unmarshal(task.Result, &result); err != nil || !result.Cancelled {
		t.Fatalf("cancelled file Task result=%s err=%v", task.Result, err)
	}
}
