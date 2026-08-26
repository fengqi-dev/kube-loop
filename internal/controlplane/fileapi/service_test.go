package fileapi_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/gorilla/websocket"

	"github.com/fengqi-dev/kube-loop/internal/controlplane"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/filestream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/testutil/websockettest"
)

type sessionValidator struct {
	identityID string
	session    sessionapi.ActiveSession
}

func serveAPI(
	t *testing.T,
	handler *fileapi.Service,
	response *httptest.ResponseRecorder,
	request *http.Request,
	identity controlplaneapi.Identity,
) *controlplaneapi.Error {
	t.Helper()
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(*http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return identity, nil
				},
			),
		),
		controlplane.WithAuthorizer(
			policy,
		),
		controlplane.WithAPIRoutes(
			controlplane.APIRoutes{
				FileTransfers: fileapi.NewRoutes(handler).Endpoints(),
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	server.Handler().ServeHTTP(response, request)
	if response.Code < http.StatusBadRequest {
		return nil
	}
	return &controlplaneapi.Error{
		Code:    mapStatus(response.Code),
		Message: response.Body.String(),
	}
}

func mapStatus(status int) controlplaneapi.ErrorCode {
	switch status {
	case http.StatusBadRequest:
		return controlplaneapi.CodeInvalidArgument
	case http.StatusNotFound:
		return controlplaneapi.CodeNotFound
	case http.StatusConflict:
		return controlplaneapi.CodeConflict
	default:
		return controlplaneapi.CodeInternal
	}
}

func (validator sessionValidator) RequireActive(
	_ context.Context,
	identity controlplaneapi.Identity,
	namespace, sessionID string,
) (sessionapi.ActiveSession, *controlplaneapi.Error) {
	if identity.Subject != validator.identityID ||
		namespace != validator.session.Namespace ||
		sessionID != validator.session.ID {
		return sessionapi.ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeNotFound,
			Message: "resource not found",
		}
	}
	return validator.session, nil
}

type targetResolver struct {
	mu    sync.Mutex
	calls int
}

type transferExecutor struct{}

func (transferExecutor) UploadOffset(
	context.Context,
	controlplaneapi.Identity,
	string,
	fileapi.Spec,
) (uint64, error) {
	return 0, nil
}

type offsetExecutor struct {
	transferExecutor

	offset uint64
	calls  int
}

func (executor *offsetExecutor) UploadOffset(
	context.Context,
	controlplaneapi.Identity,
	string,
	fileapi.Spec,
) (uint64, error) {
	executor.calls++
	return executor.offset, nil
}

func (transferExecutor) Upload(
	context.Context,
	controlplaneapi.Identity,
	string,
	string,
	fileapi.Spec,
	io.Reader,
) (fileapi.Outcome, error) {
	return fileapi.Outcome{}, nil
}

type streamExecutor struct {
	mu            sync.Mutex
	uploaded      []byte
	download      []byte
	uploadCalls   int
	downloadCalls int
}

func (*streamExecutor) UploadOffset(
	context.Context,
	controlplaneapi.Identity,
	string,
	fileapi.Spec,
) (uint64, error) {
	return 0, nil
}

func (executor *streamExecutor) Upload(
	_ context.Context,
	_ controlplaneapi.Identity,
	_, _ string,
	_ fileapi.Spec,
	input io.Reader,
) (fileapi.Outcome, error) {
	contents, err := io.ReadAll(input)
	if err != nil {
		return fileapi.Outcome{}, err
	}
	checksum := sha256.Sum256(contents)
	executor.mu.Lock()
	executor.uploaded = append([]byte(nil), contents...)
	executor.uploadCalls++
	executor.mu.Unlock()
	return fileapi.Outcome{
		Transferred: uint64(len(contents)),
		Checksum:    checksum,
		HasChecksum: true,
	}, nil
}

func (executor *streamExecutor) Download(
	_ context.Context,
	_ controlplaneapi.Identity,
	_, _ string,
	_ fileapi.Spec,
	metadata func(fileapi.DownloadMetadata) error,
	output io.Writer,
) (fileapi.Outcome, error) {
	executor.mu.Lock()
	contents := append([]byte(nil), executor.download...)
	executor.downloadCalls++
	executor.mu.Unlock()
	if err := metadata(fileapi.DownloadMetadata{Total: uint64(len(contents))}); err != nil {
		return fileapi.Outcome{}, err
	}
	if _, err := output.Write(contents); err != nil {
		return fileapi.Outcome{}, err
	}
	checksum := sha256.Sum256(contents)
	return fileapi.Outcome{
		Transferred: uint64(len(contents)),
		Checksum:    checksum,
		HasChecksum: true,
	}, nil
}

func (transferExecutor) Download(
	context.Context, controlplaneapi.Identity, string, string, fileapi.Spec,
	func(fileapi.DownloadMetadata) error, io.Writer,
) (fileapi.Outcome, error) {
	return fileapi.Outcome{}, nil
}

func (resolver *targetResolver) ResolveContainer(
	_ context.Context,
	_ controlplaneapi.Identity,
	namespace, pod, container string,
) (string, error) {
	resolver.mu.Lock()
	resolver.calls++
	resolver.mu.Unlock()
	if namespace != "development" || pod != "api-0" {
		return "", context.Canceled
	}
	if container == "" {
		return "api", nil
	}
	return container, nil
}

func TestFileTransferTaskIsValidatedOwnedAndIdempotent(t *testing.T) {
	now := time.Now().UTC()
	stateStore, identityID, sessionID, expiresAt := createStore(t, now)
	defer func() { _ = stateStore.Close() }()
	resolver := &targetResolver{}
	handler, err := fileapi.New(
		stateStore,
		sessionValidator{
			identityID: identityID,
			session: sessionapi.ActiveSession{
				ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
			},
		},
		resolver,
		transferExecutor{},
		fileapi.Config{
			Now:              func() time.Time { return now },
			AllowedPathRoots: []string{"/workspace"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{
						Subject:  request.Header.Get("X-Identity"),
						DeviceID: "device",
					}, nil
				},
			),
		),
		controlplane.WithAuthorizer(policy),
		controlplane.WithAPIRoutes(
			controlplane.APIRoutes{
				FileTransfers: fileapi.NewRoutes(handler).Endpoints(),
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()
	body := `{"direction":"upload","kind":"file","pod":"api-0","remotePath":"/workspace/data.bin","size":7,"checksum":"` + strings.Repeat(
		"ab",
		32,
	) + `","overwrite":true}`
	document := createTask(
		t,
		httpServer.URL,
		identityID,
		sessionID,
		"file-1",
		body,
		http.StatusCreated,
	)
	if document.State != "pending" || document.Container != "api" ||
		document.RemotePath != "/workspace/data.bin" ||
		document.Size != 7 {
		t.Fatalf("created document = %#v", document)
	}
	replayed := createTask(
		t,
		httpServer.URL,
		identityID,
		sessionID,
		"file-1",
		body,
		http.StatusOK,
	)
	if replayed.ID != document.ID {
		t.Fatalf("replayed task = %#v, want %s", replayed, document.ID)
	}
	resolver.mu.Lock()
	if resolver.calls != 1 {
		t.Fatalf("target resolver calls = %d", resolver.calls)
	}
	resolver.mu.Unlock()
	getRequest, _ := http.NewRequest(
		http.MethodGet,
		httpServer.URL+"/api/sessions/"+sessionID+"/file-transfers/"+document.ID+"?namespace=development",
		nil,
	)
	getRequest.Header.Set("X-Identity", identityID)
	getResponse, err := http.DefaultClient.Do(getRequest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = getResponse.Body.Close() }()
	if getResponse.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d", getResponse.StatusCode)
	}
	conflict := createTask(
		t,
		httpServer.URL,
		identityID,
		sessionID,
		"file-1",
		strings.Replace(body, `"size":7`, `"size":8`, 1),
		http.StatusConflict,
	)
	if conflict.ID != "" {
		t.Fatalf("conflicting task was returned: %#v", conflict)
	}
}

func TestFileTransferRejectsTraversalRootAndUntrustedDownloadMetadata(
	t *testing.T,
) {
	now := time.Now().UTC()
	stateStore, identityID, sessionID, expiresAt := createStore(t, now)
	defer func() { _ = stateStore.Close() }()
	resolver := &targetResolver{}
	handler, err := fileapi.New(
		stateStore,
		sessionValidator{
			identityID: identityID,
			session: sessionapi.ActiveSession{
				ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
			},
		},
		resolver,
		transferExecutor{},
		fileapi.Config{
			AllowedPathRoots: []string{"/workspace"},
			MaximumBytes:     1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range []fileapi.Spec{
		{Direction: "upload", Kind: "file", Pod: "api-0", RemotePath: "/workspace/../etc/passwd", Size: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: "upload", Kind: "file", Pod: "api-0", RemotePath: "/etc/passwd", Size: 1, Checksum: strings.Repeat("00", 32)},
		{Direction: "upload", Kind: "file", Pod: "api-0", RemotePath: "/workspace/huge", Size: 2 << 20, Checksum: strings.Repeat("00", 32)},
		{Direction: "download", Kind: "file", Pod: "api-0", RemotePath: "/workspace/data", Size: 1, Checksum: strings.Repeat("00", 32)},
	} {
		raw, _ := json.Marshal(spec)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/sessions/"+sessionID+"/file-transfers?namespace=development",
			bytes.NewReader(raw),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", uuid.NewString())
		response := httptest.NewRecorder()
		apiError := serveAPI(
			t,
			handler,
			response,
			request,
			controlplaneapi.Identity{Subject: identityID, DeviceID: "device"},
		)
		if apiError == nil ||
			apiError.Code != controlplaneapi.CodeInvalidArgument {
			t.Fatalf("spec %#v error = %#v", spec, apiError)
		}
	}
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if resolver.calls != 0 {
		t.Fatalf(
			"invalid requests reached Kubernetes resolver: %d",
			resolver.calls,
		)
	}
}

func TestFileTransferTaskUsesControlPlaneAuthoritativeResumeOffset(
	t *testing.T,
) {
	now := time.Now().UTC()
	stateStore, identityID, sessionID, expiresAt := createStore(t, now)
	defer func() { _ = stateStore.Close() }()
	executor := &offsetExecutor{offset: 7}
	handler, err := fileapi.New(
		stateStore,
		sessionValidator{
			identityID: identityID,
			session: sessionapi.ActiveSession{
				ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
			},
		},
		&targetResolver{},
		executor,
		fileapi.Config{
			AllowedPathRoots: []string{"/workspace"},
			MaximumBytes:     1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	resumeID := uuid.NewString()
	body := `{"direction":"upload","kind":"file","pod":"api-0","remotePath":"/workspace/data.bin","size":20,"checksum":"` + strings.Repeat(
		"ab",
		32,
	) + `","resumeId":"` + resumeID + `"}`
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/sessions/"+sessionID+"/file-transfers?namespace=development",
		strings.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "resume-offset")
	response := httptest.NewRecorder()
	if apiError := serveAPI(
		t,
		handler,
		response,
		request,
		controlplaneapi.Identity{Subject: identityID},
	); apiError != nil {
		t.Fatal(apiError)
	}
	var document fileapi.Document
	if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusCreated || document.Offset != 7 ||
		document.ResumeID != resumeID ||
		executor.calls != 1 {
		t.Fatalf("document = %#v resume calls = %d", document, executor.calls)
	}
}

func TestFileTransferWebSocketUploadDownloadAndSingleClaim(t *testing.T) {
	now := time.Now().UTC()
	stateStore, identityID, sessionID, expiresAt := createStore(t, now)
	defer func() { _ = stateStore.Close() }()
	executor := &streamExecutor{download: []byte("gateway download")}
	handler, err := fileapi.New(
		stateStore,
		sessionValidator{
			identityID: identityID,
			session: sessionapi.ActiveSession{
				ID: sessionID, Namespace: "development", ExpiresAt: expiresAt,
			},
		},
		&targetResolver{},
		executor,
		fileapi.Config{
			Now: func() time.Time { return now }, AllowedPathRoots: []string{"/workspace"}, MaximumBytes: 1 << 20,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	policy := authorization.NewAuthenticated()
	server, err := controlplane.NewServer(
		controlplane.Config{PublicURL: "https://gateway.example.test"},
		controlplane.BuildInfo{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
		controlplane.WithAuthenticator(
			controlplaneapi.AuthenticatorFunc(
				func(request *http.Request) (controlplaneapi.Identity, *controlplaneapi.Error) {
					return controlplaneapi.Identity{
						Subject:  request.Header.Get("X-Identity"),
						DeviceID: "device",
					}, nil
				},
			),
		),
		controlplane.WithAuthorizer(policy),
		controlplane.WithAPIRoutes(
			controlplane.APIRoutes{
				FileTransfers: fileapi.NewRoutes(handler).Endpoints(),
			},
		),
	)
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	upload := []byte("gateway upload")
	uploadChecksum := sha256.Sum256(upload)
	uploadBody, _ := json.Marshal(fileapi.Spec{
		Direction: fileapi.DirectionUpload, Kind: fileapi.KindFile, Pod: "api-0",
		RemotePath: "/workspace/upload.bin", Size: uint64(len(upload)),
		Checksum: filestream.FormatChecksum(uploadChecksum), Overwrite: true,
	})
	uploadTask := createTask(
		t,
		httpServer.URL,
		identityID,
		sessionID,
		"upload-stream",
		string(uploadBody),
		http.StatusCreated,
	)
	uploadURL := fileStreamURL(httpServer.URL, sessionID, uploadTask.ID)
	uploadConnection := dialFileStream(t, uploadURL, identityID)
	dataFrame, _ := filestream.Encode(
		filestream.Frame{Type: filestream.Data, Payload: upload},
	)
	if err := uploadConnection.WriteMessage(websocket.BinaryMessage, dataFrame); err != nil {
		t.Fatal(err)
	}
	completeFrame, _ := filestream.Encode(
		filestream.Frame{Type: filestream.Complete},
	)
	if err := uploadConnection.WriteMessage(websocket.BinaryMessage, completeFrame); err != nil {
		t.Fatal(err)
	}
	uploadResult, uploadProgress, _ := readFileStream(t, uploadConnection)
	if uploadResult.Status != filestream.ResultSucceeded ||
		uploadResult.Transferred != uint64(len(upload)) ||
		!uploadResult.HasChecksum ||
		uploadResult.Checksum != uploadChecksum ||
		uploadProgress.Transferred != uint64(len(upload)) {
		t.Fatalf(
			"upload result = %#v progress = %#v",
			uploadResult,
			uploadProgress,
		)
	}
	_ = uploadConnection.Close()
	storedUpload, err := stateStore.Tasks().
		GetByID(context.Background(), uploadTask.ID)
	if err != nil || storedUpload.State != "stopped" {
		t.Fatalf("stored upload = %#v err = %v", storedUpload, err)
	}
	_, replayResponse, replayErr := websockettest.Dial(
		context.Background(),
		uploadURL,
		&websockettest.DialOptions{
			HTTPHeader: http.Header{"X-Identity": {identityID}},
		})

	if replayErr == nil || replayResponse == nil ||
		replayResponse.StatusCode != http.StatusConflict {
		t.Fatalf(
			"upload replay response = %#v err = %v",
			replayResponse,
			replayErr,
		)
	}

	downloadBody, _ := json.Marshal(fileapi.Spec{
		Direction:  fileapi.DirectionDownload,
		Kind:       fileapi.KindFile,
		Pod:        "api-0",
		RemotePath: "/workspace/download.bin",
	})
	downloadTask := createTask(
		t,
		httpServer.URL,
		identityID,
		sessionID,
		"download-stream",
		string(downloadBody),
		http.StatusCreated,
	)
	downloadConnection := dialFileStream(
		t,
		fileStreamURL(httpServer.URL, sessionID, downloadTask.ID),
		identityID,
	)
	downloadResult, downloadProgress, downloaded := readFileStream(
		t,
		downloadConnection,
	)
	downloadChecksum := sha256.Sum256(executor.download)
	if string(downloaded) != string(executor.download) ||
		downloadResult.Status != filestream.ResultSucceeded ||
		downloadResult.Checksum != downloadChecksum ||
		downloadProgress.Total != uint64(len(executor.download)) {
		t.Fatalf(
			"download bytes = %q result = %#v progress = %#v",
			downloaded,
			downloadResult,
			downloadProgress,
		)
	}
	_ = downloadConnection.Close()
	executor.mu.Lock()
	defer executor.mu.Unlock()
	if string(executor.uploaded) != string(upload) ||
		executor.uploadCalls != 1 ||
		executor.downloadCalls != 1 {
		t.Fatalf(
			"executor upload = %q upload calls = %d download calls = %d",
			executor.uploaded,
			executor.uploadCalls,
			executor.downloadCalls,
		)
	}
}

func dialFileStream(
	t *testing.T,
	streamURL, identityID string,
) *websocket.Conn {
	t.Helper()
	connection, response, err := websockettest.Dial(
		context.Background(),
		streamURL,
		&websockettest.DialOptions{
			HTTPHeader: http.Header{"X-Identity": {identityID}},
		})

	if err != nil {
		if response != nil {
			t.Fatalf("dial status = %d err = %v", response.StatusCode, err)
		}
		t.Fatal(err)
	}
	return connection
}

func fileStreamURL(baseURL, sessionID, taskID string) string {
	return "ws" + strings.TrimPrefix(
		baseURL,
		"http",
	) + "/api/sessions/" + sessionID +
		"/file-transfers/" + taskID + "/stream?namespace=development"
}

func readFileStream(
	t *testing.T,
	connection *websocket.Conn,
) (filestream.TransferResult, filestream.ProgressStatus, []byte) {
	t.Helper()
	var result filestream.TransferResult
	var progress filestream.ProgressStatus
	var contents []byte
	for {
		messageType, encoded, err := connection.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		if messageType != websocket.BinaryMessage {
			t.Fatalf("message type = %v", messageType)
		}
		frame, err := filestream.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		switch frame.Type {
		case filestream.Data:
			contents = append(contents, frame.Payload...)
		case filestream.Progress:
			progress, err = filestream.DecodeProgress(frame)
			if err != nil {
				t.Fatal(err)
			}
		case filestream.Result:
			result, err = filestream.DecodeResult(frame)
			if err != nil {
				t.Fatal(err)
			}
			return result, progress, contents
		default:
			t.Fatalf("unexpected file frame type %d", frame.Type)
		}
	}
}

func createStore(
	t *testing.T,
	now time.Time,
) (*storage.Store, string, string, time.Time) {
	t.Helper()
	stateStore, err := storage.Open(context.Background(), storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "files.db"), ControlPlaneReplicas: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	identityID, sessionID := uuid.NewString(), uuid.NewString()
	if _, err := stateStore.Identities().Create(context.Background(), storage.Identity{
		ID: identityID, Type: "human", DisplayName: "Test Identity", Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	network, _ := networkspec.Normalize(
		networkspec.Spec{PodCIDRs: []string{"10.244.0.0/16"}},
	)
	networkJSON, _ := networkspec.CanonicalJSON(network)
	networkHash, _ := networkspec.Hash(network)
	expiresAt := now.Add(time.Hour)
	if err := stateStore.Sessions().Create(context.Background(), storage.Session{
		ID: sessionID, IdentityID: identityID, DeviceID: "device", ClusterID: "cluster",
		Namespace: "development", State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	return stateStore, identityID, sessionID, expiresAt
}

func createTask(
	t *testing.T,
	baseURL, identityID, sessionID, key, body string,
	wantStatus int,
) fileapi.Document {
	t.Helper()
	request, _ := http.NewRequest(
		http.MethodPost,
		baseURL+"/api/sessions/"+sessionID+"/file-transfers?namespace=development",
		strings.NewReader(body),
	)
	request.Header.Set("X-Identity", identityID)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != wantStatus {
		contents, _ := io.ReadAll(response.Body)
		t.Fatalf(
			"create status = %d, want %d: %s",
			response.StatusCode,
			wantStatus,
			contents,
		)
	}
	var document fileapi.Document
	if response.StatusCode == http.StatusCreated ||
		response.StatusCode == http.StatusOK {
		if err := json.NewDecoder(response.Body).Decode(&document); err != nil {
			t.Fatal(err)
		}
	}
	return document
}
