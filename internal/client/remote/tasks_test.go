package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

func TestPreviewTaskRequiresClusterIPOnlyAfterItIsRunning(t *testing.T) {
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	task := PreviewTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: remoteTaskPending,
		Name: "local-api", Ports: []PreviewPort{{ServicePort: 80, Protocol: remoteProtocolTCP}},
		CreatedAt: now, UpdatedAt: now,
	}
	if _, err := validatePreviewTask(task, session); err != nil {
		t.Fatalf("pending Preview without ClusterIP was rejected: %v", err)
	}
	task.State = "running"
	if _, err := validatePreviewTask(task, session); err == nil {
		t.Fatal("running Preview without ClusterIP was accepted")
	}
	task.ClusterIP = "10.96.0.42"
	if _, err := validatePreviewTask(task, session); err != nil {
		t.Fatalf("running Preview with ClusterIP was rejected: %v", err)
	}
}

func TestPodExecTaskOpensAuthenticatedWebSocketStream(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID:         uuid.NewString(),
		Namespace:  "development",
		State:      remoteSessionActive,
		Generation: 1,
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}
	task := ExecTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace, State: remoteTaskPending,
		Pod: "api-0", Container: "api", CreatedAt: now, UpdatedAt: now, ExpiresAt: session.ExpiresAt,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "exec-key" ||
				request.Header.Get("Authorization") != "Bearer access-token" {
				t.Errorf("headers = %#v", request.Header)
			}
			_ = json.NewEncoder(writer).Encode(task)
			return
		}
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = connection.Close() }()
		stdout, _ := execstream.Encode(execstream.Frame{Type: execstream.Stdout, Payload: []byte("hello")})
		exit, _ := execstream.EncodeExit(execstream.ExitStatus{})
		_ = connection.WriteMessage(websocket.BinaryMessage, stdout)
		_ = connection.WriteMessage(websocket.BinaryMessage, exit)
		_ = connection.Close()
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	created, err := client.CreateExecTask(context.Background(), serverProfile, session, ExecSpec{
		Pod: "api-0", Container: "api", Command: []string{"/bin/sh"},
	}, "exec-key")
	if err != nil || created.ID != task.ID {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	connection, err := client.OpenExecStream(context.Background(), serverProfile, session, created)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.Close)
	_, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := execstream.Decode(encoded)
	if err != nil || frame.Type != execstream.Stdout || string(frame.Payload) != "hello" {
		t.Fatalf("frame = %#v err = %v", frame, err)
	}
}

func TestFileTransferTaskUsesAuthenticatedControlAndWebSocketAPIs(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	task := FileTransferTask{
		ID:         uuid.NewString(),
		SessionID:  session.ID,
		Namespace:  session.Namespace,
		State:      remoteTaskPending,
		Direction:  remoteDirectionDownload,
		Kind:       remoteKindFile,
		Pod:        "api-0",
		Container:  "api",
		RemotePath: "/workspace/data.bin",
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  session.ExpiresAt,
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		if strings.HasSuffix(request.URL.Path, "/stream") {
			connection, err := (&websocket.Upgrader{}).Upgrade(writer, request, nil)
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = connection.Close() }()
			progress, _ := filestream.EncodeProgress(filestream.ProgressStatus{Total: 4})
			result, _ := filestream.EncodeResult(
				filestream.TransferResult{Status: filestream.ResultSucceeded, Transferred: 4},
			)
			_ = connection.WriteMessage(websocket.BinaryMessage, progress)
			_ = connection.WriteMessage(websocket.BinaryMessage, result)
			return
		}
		if request.Method == http.MethodPost {
			if request.Header.Get(remoteHeaderIdempotencyKey) != "file-key" {
				t.Errorf("Idempotency-Key = %q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			var spec FileTransferSpec
			if err := json.NewDecoder(request.Body).
				Decode(&spec); err != nil || spec.RemotePath != task.RemotePath ||
				spec.Direction != remoteDirectionDownload {
				t.Errorf("spec = %#v err = %v", spec, err)
			}
		}
		_ = json.NewEncoder(writer).Encode(task)
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	created, err := client.CreateFileTransferTask(context.Background(), serverProfile, session, FileTransferSpec{
		Direction: remoteDirectionDownload, Kind: remoteKindFile, Pod: "api-0", RemotePath: task.RemotePath,
	}, "file-key")
	if err != nil || created.ID != task.ID {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	loaded, err := client.GetFileTransferTask(context.Background(), serverProfile, session, task.ID)
	if err != nil || loaded.ID != task.ID {
		t.Fatalf("loaded = %#v err = %v", loaded, err)
	}
	connection, err := client.OpenFileTransferStream(context.Background(), serverProfile, session, created)
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, connection.Close)
	_, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	frame, err := filestream.Decode(encoded)
	if err != nil || frame.Type != filestream.Progress {
		t.Fatalf("frame = %#v err = %v", frame, err)
	}
}

func TestFileTransferClientRejectsUnsafeLocalSpecAndGatewayTask(t *testing.T) {
	for _, spec := range []FileTransferSpec{
		{Direction: remoteDirectionUpload, Kind: remoteKindFile, Pod: "api-0", RemotePath: "../escape", Size: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: remoteDirectionUpload, Kind: remoteKindDirectory, Pod: "api-0", RemotePath: "/workspace/data", Size: 1, Offset: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: remoteDirectionDownload, Kind: remoteKindFile, Pod: "api-0", RemotePath: "/workspace/data", Size: 1},
	} {
		if err := validateFileTransferSpec(&spec); err == nil {
			t.Fatalf("unsafe spec was accepted: %#v", spec)
		}
	}
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	task := FileTransferTask{
		ID:         uuid.NewString(),
		SessionID:  session.ID,
		Namespace:  session.Namespace,
		State:      remoteTaskPending,
		Direction:  remoteDirectionDownload,
		Kind:       remoteKindFile,
		Pod:        "api-0",
		Container:  "api",
		RemotePath: "/workspace/../escape",
		CreatedAt:  now,
		UpdatedAt:  now,
		ExpiresAt:  now.Add(time.Minute),
	}
	if _, err := validateFileTransferTask(task, session); err == nil {
		t.Fatal("unsafe Gateway task was accepted")
	}
}

func TestPodFileClientUsesSessionBoundListAndIdempotentMutationAPIs(t *testing.T) {
	now := time.Now().UTC()
	store := &memoryStore{value: validCredential(now)}
	session := Session{
		ID: uuid.NewString(), Namespace: "development", State: remoteSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}
	taskID := uuid.NewString()
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer access-token" ||
			request.URL.Query().Get(remoteParamNamespace) != session.Namespace {
			t.Errorf("request headers/query = %#v %s", request.Header, request.URL.RawQuery)
		}
		switch {
		case strings.HasSuffix(request.URL.Path, "/list"):
			var spec PodFileSpec
			if err := json.NewDecoder(request.Body).Decode(&spec); err != nil || spec.Path != "/workspace" {
				t.Errorf("list spec = %#v err = %v", spec, err)
			}
			_ = json.NewEncoder(writer).Encode(PodFileList{
				SessionID: session.ID,
				Namespace: session.Namespace,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace",
				Items: []PodFileEntry{
					{Name: "logs", Path: "/workspace/logs", Kind: remoteKindDirectory, Mode: "0755", ModifiedAt: now},
				},
			})
		case strings.HasSuffix(request.URL.Path, "/create"):
			if request.Header.Get(remoteHeaderIdempotencyKey) != "pod-file-key" {
				t.Errorf("Idempotency-Key = %q", request.Header.Get(remoteHeaderIdempotencyKey))
			}
			_ = json.NewEncoder(writer).Encode(PodFileTask{
				ID:        taskID,
				SessionID: session.ID,
				Namespace: session.Namespace,
				State:     "stopped",
				Action:    remoteActionCreate,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace/new",
				Kind:      remoteKindDirectory,
				Result:    PodFileResult{Completed: true},
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: session.ExpiresAt,
			})
		case strings.Contains(request.URL.Path, "/operations/"):
			_ = json.NewEncoder(writer).Encode(PodFileTask{
				ID:        taskID,
				SessionID: session.ID,
				Namespace: session.Namespace,
				State:     "stopped",
				Action:    remoteActionCreate,
				Pod:       "api-0",
				Container: "api",
				Path:      "/workspace/new",
				Kind:      remoteKindDirectory,
				Result:    PodFileResult{Completed: true},
				CreatedAt: now,
				UpdatedAt: now,
				ExpiresAt: session.ExpiresAt,
			})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := New(
		store,
		&fakeRefresher{now: now},
		Config{HTTPClient: server.Client(), Now: func() time.Time { return now }},
	)
	if err != nil {
		t.Fatal(err)
	}
	serverProfile := profile.Profile{ID: "service-1", BaseURL: server.URL}
	listing, err := client.ListPodFiles(
		context.Background(),
		serverProfile,
		session,
		PodFileSpec{Pod: "api-0", Path: "/workspace"},
	)
	if err != nil || len(listing.Items) != 1 || listing.Items[0].Name != "logs" {
		t.Fatalf("listing = %#v err = %v", listing, err)
	}
	created, err := client.CreatePodFileOperation(
		context.Background(),
		serverProfile,
		session,
		remoteActionCreate,
		PodFileSpec{
			Pod: "api-0", Path: "/workspace/new", Kind: remoteKindDirectory,
		},
		"pod-file-key",
	)
	if err != nil || created.ID != taskID || !created.Result.Completed {
		t.Fatalf("created = %#v err = %v", created, err)
	}
	loaded, err := client.GetPodFileOperation(context.Background(), serverProfile, session, taskID)
	if err != nil || loaded.ID != taskID {
		t.Fatalf("loaded = %#v err = %v", loaded, err)
	}
}

func TestPodFileClientRejectsUnsafeSpecsAndUnboundResponses(t *testing.T) {
	for action, spec := range map[string]PodFileSpec{
		remoteActionList:   {Pod: "api-0", Path: "/workspace/../etc"},
		remoteActionCreate: {Pod: "api-0", Path: "/", Kind: remoteKindFile},
		"rename":           {Pod: "api-0", Path: "/workspace/a", Destination: "/"},
		remoteActionDelete: {Pod: "api-0", Path: "relative"},
	} {
		if err := validatePodFileSpec(action, &spec); err == nil {
			t.Fatalf("unsafe %s spec accepted: %#v", action, spec)
		}
	}
	now := time.Now().UTC()
	session := Session{ID: uuid.NewString(), Namespace: "development"}
	_, err := validatePodFileList(PodFileList{
		SessionID: uuid.NewString(),
		Namespace: session.Namespace,
		Pod:       "api-0",
		Container: "api",
		Path:      "/workspace",
		Items:     []PodFileEntry{},
	}, session, PodFileSpec{Pod: "api-0", Path: "/workspace"})
	if err == nil {
		t.Fatal("listing bound to another Session was accepted")
	}
	_, err = validatePodFileTask(PodFileTask{
		ID:        uuid.NewString(),
		SessionID: session.ID,
		Namespace: session.Namespace,
		State:     "failed",
		Action:    remoteActionDelete,
		Pod:       "api-0",
		Container: "api",
		Path:      "/workspace/a",
		Result:    PodFileResult{},
		CreatedAt: now,
		UpdatedAt: now,
		ExpiresAt: now.Add(time.Minute),
	}, session)
	if err == nil {
		t.Fatal("failed Task without a bounded error was accepted")
	}
}
