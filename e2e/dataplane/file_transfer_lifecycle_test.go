//go:build e2e

package dataplane

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
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientfiletransfer "github.com/fengqi-dev/kube-loop/internal/client/filetransfer"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/execapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileopsapi"
	controlplanekubernetes "github.com/fengqi-dev/kube-loop/internal/controlplane/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/google/uuid"
	"github.com/kballard/go-shellquote"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fileE2EIdentity struct {
	identity        controlplaneapi.Identity
	active          sessionapi.ActiveSession
	session         remote.Session
	authorizationID string
	token           string
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
		t.Fatalf("listen for file Control Plane: %v", err)
	}
	handler, err := fileapi.New(
		stateStore,
		e2eExecSessionValidator{identityID: identity.identity.Subject, session: identity.active},
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
		e2eExecSessionValidator{identityID: identity.identity.Subject, session: identity.active},
		targets,
		fileOperations,
		fileopsapi.Config{AllowedPathRoots: []string{"/tmp"}},
	)
	if err != nil {
		_ = listener.Close()
		t.Fatal(err)
	}
	routes := controlplane.APIRoutes{
		FileTransfers:  fileapi.NewRoutes(handler).Endpoints(),
		FileOperations: fileopsapi.NewRoutes(operations).Endpoints(),
	}
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "http://" + listener.Addr().String()}, controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(controlplaneapi.AuthenticatorFunc(func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
			if request.Header.Get("Authorization") != "Bearer "+identity.token {
				return controlplaneapi.Identity{}, &controlplaneapi.Error{Code: controlplaneapi.CodeUnauthenticated, Message: "invalid file E2E token"}
			}
			return identity.identity, nil
		})),
		controlplane.WithAuthorizer(policy), controlplane.WithAPIRoutes(routes),
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
	identityID, deviceID, namespace string,
	network networkspec.Spec,
	networkHash, token string,
	hashByte byte,
) fileE2EIdentity {
	t.Helper()
	now := time.Now().UTC()
	authorizationID, sessionID := uuid.NewString(), uuid.NewString()
	expiresAt := now.Add(5 * time.Minute)
	createOAuthGrant(t, ctx, stateStore, authorizationID, identityID, deviceID, hashByte, now, expiresAt)
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: namespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return fileE2EIdentity{
		identity: controlplaneapi.Identity{
			Subject: identityID, DeviceID: deviceID, AuthorizationID: authorizationID, AccessExpiresAt: expiresAt,
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
		authorizationID: authorizationID,
		token:           token,
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
	identityID, deviceID := uuid.NewString(), "e2e-file-device"
	if _, err := stateStore.Identities().Create(ctx, storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now, UpdatedAt: now,
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
	provider, err := controlplanekubernetes.NewForRESTConfig(kubeRESTConfig(t), controlplanekubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	targets, err := controlplanekubernetes.NewContainerResolver(provider)
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
		t, ctx, stateStore, identityID, deviceID, harness.EchoNamespace,
		network, networkHash, "e2e-file-revoked", 5,
	)
	running := startFileController(t, "127.0.0.1:0", stateStore, targets, transfers, fileOperations, firstIdentity)
	controllerAddress := running.Address()
	controllerStopped := false
	t.Cleanup(func() {
		if !controllerStopped {
			running.Stop(t)
		}
	})
	serverProfile := profile.Profile{ID: "e2e-file", BaseURL: "http://" + controllerAddress}
	firstClient := newFileRemoteClient(t, serverProfile.ID, firstIdentity)
	payload := bytes.Repeat([]byte("KubeLoop-file-transfer\n"), (8<<20)/len("KubeLoop-file-transfer\n"))
	checksum := sha256.Sum256(payload)
	testID := uuid.NewString()
	revokedPath := "/tmp/kubeloop-revoked-" + testID
	restartPath := "/tmp/kubeloop-restart-" + testID
	revokedResumeID, restartResumeID := uuid.NewString(), uuid.NewString()
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		command := "rm -f -- " + strings.Join([]string{
			revokedPath, revokedPath + ".kubeloop-" + revokedResumeID + ".part",
			restartPath, restartPath + ".kubeloop-" + restartResumeID + ".part",
		}, " ")
		_ = podExecutor.Exec(cleanupContext, firstIdentity.identity, harness.EchoNamespace, execapi.Spec{
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
	waitForFileProgress(t, ctx, firstProgress, firstUpload)
	revokedAt := time.Now()
	if err := stateStore.OAuthSessions().RevokeRequest(ctx, firstIdentity.authorizationID, revokedAt.UTC()); err != nil {
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
		t, ctx, stateStore, identityID, deviceID, harness.EchoNamespace,
		network, networkHash, "e2e-file-active", 6,
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
	waitForFileProgress(t, ctx, restartProgress, restartUpload)
	running.Stop(t)
	controllerStopped = true
	restartResult := waitForFileUpload(t, ctx, restartUpload)
	if restartResult.err == nil || restartResult.task.ID == "" {
		t.Fatalf("upload unexpectedly survived Control Plane restart: task=%#v result=%#v err=%v", restartResult.task, restartResult.result, restartResult.err)
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
		t.Fatalf("resume real upload after Control Plane restart: task=%#v result=%#v err=%v", completedTask, completedResult, err)
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
	specialRoot := "/tmp/kubeloop-special-" + testID
	remoteFile := specialRoot + "/file ' $;.txt"
	renamedFile := specialRoot + "/renamed ' $;.txt"
	remoteDirectory := specialRoot + "/directory ' $;"
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = podExecutor.Exec(cleanupContext, secondIdentity.identity, harness.EchoNamespace, execapi.Spec{
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
	directoryContents := []byte("KubeLoop directory transfer\n")
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
			TokenType: "Bearer", AccessToken: identity.token, AccessExpiresAt: identity.identity.AccessExpiresAt,
			RefreshToken: "unused", RefreshExpiresAt: identity.identity.AccessExpiresAt,
			DeviceID: identity.identity.DeviceID,
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

func waitForFileProgress(
	t *testing.T,
	ctx context.Context,
	progress <-chan filestream.ProgressStatus,
	result <-chan fileUploadResult,
) {
	t.Helper()
	for {
		select {
		case value := <-progress:
			if value.Transferred > 0 {
				return
			}
		case value := <-result:
			t.Fatalf("real file upload ended before reporting progress: task=%#v result=%#v err=%v", value.task, value.result, value.err)
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
