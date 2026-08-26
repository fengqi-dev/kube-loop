package podssh

import (
	"context"
	"crypto/ed25519"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/gorilla/websocket"

	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/fengqi-dev/kube-loop/internal/testutil/websockettest"
)

type fakeHostTCPRegistrar struct {
	mu       sync.Mutex
	handlers map[string]socksbridge.HostTCPHandler
}

type blockingHostTCPRegistrar struct {
	*fakeHostTCPRegistrar

	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func (registrar *blockingHostTCPRegistrar) SetHostTCPHandler(
	profileID string,
	handler socksbridge.HostTCPHandler,
) error {
	registrar.startedOnce.Do(func() { close(registrar.started) })
	<-registrar.release
	return registrar.fakeHostTCPRegistrar.SetHostTCPHandler(profileID, handler)
}

func (registrar *blockingHostTCPRegistrar) unblock() {
	registrar.releaseOnce.Do(func() { close(registrar.release) })
}

func (registrar *fakeHostTCPRegistrar) SetHostTCPHandler(profileID string, handler socksbridge.HostTCPHandler) error {
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if registrar.handlers == nil {
		registrar.handlers = make(map[string]socksbridge.HostTCPHandler)
	}
	registrar.handlers[profileID] = handler
	return nil
}

func (registrar *fakeHostTCPRegistrar) dial() (net.Conn, error) {
	registrar.mu.Lock()
	handler := registrar.handlers["server-a"]
	registrar.mu.Unlock()
	if handler == nil {
		return nil, errors.New("host TCP handler is unavailable")
	}
	serve, ok := handler("10.244.1.7", 22)
	if !ok || serve == nil {
		return nil, errors.New("host TCP destination was not claimed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	client, err := net.DialTimeout("tcp", listener.Addr().String(), time.Second)
	if err != nil {
		_ = listener.Close()
		return nil, err
	}
	server, err := listener.Accept()
	_ = listener.Close()
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	go serve(server)
	return client, nil
}

type fakeSessions struct {
	mu       sync.Mutex
	sessions map[string]remote.Session
}

func (source *fakeSessions) Current(profileID string) (remote.Session, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.sessions[profileID], nil
}

type fakeExecClient struct {
	server *httptest.Server
	mu     sync.Mutex
	specs  []remote.ExecSpec
	sizes  []execstream.TerminalSize
}

func newFakeExecClient(t *testing.T) *fakeExecClient {
	t.Helper()
	client := &fakeExecClient{}
	client.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := websockettest.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer checkTestClose(t, connection.Close)
		if err := connection.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Error(err)
			return
		}
		for {
			_, encoded, err := connection.ReadMessage()
			if err != nil {
				return
			}
			frame, err := execstream.Decode(encoded)
			if err != nil {
				return
			}
			if frame.Type != execstream.Resize {
				continue
			}
			size, err := execstream.DecodeResize(frame)
			if err != nil {
				return
			}
			client.mu.Lock()
			client.sizes = append(client.sizes, size)
			client.mu.Unlock()
			stdout, _ := execstream.Encode(execstream.Frame{Type: execstream.Stdout, Payload: []byte("remote-shell\n")})
			if err := connection.WriteMessage(websocket.BinaryMessage, stdout); err != nil {
				return
			}
			exit, _ := execstream.EncodeExit(execstream.ExitStatus{})
			if err := connection.WriteMessage(websocket.BinaryMessage, exit); err != nil {
				return
			}
			// Let the client consume the terminal frame and initiate the close
			// handshake. Closing the TCP socket here can surface as WSAECONNRESET
			// before the buffered exit frame is observed on Windows.
			for {
				if _, _, err := connection.ReadMessage(); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(client.server.Close)
	return client
}

func (client *fakeExecClient) CreateExecTask(
	_ context.Context,
	_ profile.Profile,
	session remote.Session,
	spec remote.ExecSpec,
	_ string,
) (remote.ExecTask, error) {
	client.mu.Lock()
	client.specs = append(client.specs, spec)
	client.mu.Unlock()
	now := time.Now().UTC()
	return remote.ExecTask{
		ID: uuid.NewString(), SessionID: session.ID, Namespace: session.Namespace,
		State: "pending", Pod: spec.Pod, Container: spec.Container, TTY: spec.TTY,
		CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Minute),
	}, nil
}

func (client *fakeExecClient) OpenExecStream(
	ctx context.Context,
	_ profile.Profile,
	_ remote.Session,
	_ remote.ExecTask,
) (*websocket.Conn, error) {
	endpoint := "ws" + strings.TrimPrefix(client.server.URL, "http")
	connection, _, err := websockettest.Dial(ctx, endpoint, nil)
	return connection, err
}

func TestManagerServesNativePodIPSSHThroughRemoteExec(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	client := newFakeExecClient(t)
	now := time.Now().UTC()
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: podSSHSessionActive, Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}
	sessions := &fakeSessions{sessions: map[string]remote.Session{"server-a": session}}
	registrar := &fakeHostTCPRegistrar{}
	manager, err := New(client, sessions, Config{
		ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)}, HostTCPRegistrar: registrar,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	info, err := manager.Start(
		context.Background(),
		profile.Profile{ID: "server-a", BaseURL: "https://gateway.test"},
		session,
		Request{
			ProfileID: "server-a", Namespace: "development", Pod: "api-0", Container: "main",
			PodIP: "10.244.1.7", Ready: true, Containers: []string{"main", "sidecar"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if info.Address != "10.244.1.7:22" || info.Port != 22 || strings.Contains(info.Command, " -p ") ||
		!strings.Contains(info.Command, "main@10.244.1.7") {
		t.Fatalf("Pod SSH info = %#v", info)
	}
	raw, err := registrar.dial()
	if err != nil {
		t.Fatal(err)
	}
	connection, channels, requests, err := ssh.NewClientConn(raw, info.Address, &ssh.ClientConfig{
		User: "main", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	sshClient := ssh.NewClient(connection, channels, requests)
	defer checkTestClose(t, sshClient.Close)
	sshSession, err := sshClient.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer checkTestClose(t, sshSession.Close)
	if err := sshSession.RequestPty("xterm", 40, 120, ssh.TerminalModes{}); err != nil {
		t.Fatal(err)
	}
	output, err := sshSession.CombinedOutput("echo hello")
	if err != nil {
		t.Fatalf("run local Pod SSH command: %v output=%q", err, output)
	}
	if string(output) != "remote-shell\n" {
		t.Fatalf("Pod SSH output = %q", output)
	}
	client.mu.Lock()
	specs := append([]remote.ExecSpec(nil), client.specs...)
	sizes := append([]execstream.TerminalSize(nil), client.sizes...)
	client.mu.Unlock()
	if len(specs) != 1 || specs[0].Pod != "api-0" || specs[0].Container != "main" ||
		!specs[0].TTY || strings.Join(specs[0].Command, " ") != "/bin/sh -c echo hello" {
		t.Fatalf("remote exec specs = %#v", specs)
	}
	if len(sizes) != 1 || sizes[0].Width != 120 || sizes[0].Height != 40 {
		t.Fatalf("remote terminal sizes = %#v", sizes)
	}
	if err := manager.Stop("server-a", info.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := registrar.dial(); err == nil {
		t.Fatal("disabled PodIP:22 remained claimed")
	}
}

func TestManagerRejectsStartAfterShutdown(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: podSSHSessionActive,
	}
	manager, err := New(
		newFakeExecClient(t),
		&fakeSessions{sessions: map[string]remote.Session{"server-a": session}},
		Config{ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Start(
		t.Context(),
		profile.Profile{ID: "server-a"},
		session,
		Request{
			ProfileID: "server-a", Namespace: "development", Pod: "api-0",
			PodIP: "10.244.1.7", Ready: true, Containers: []string{"main"},
		},
	); !errors.Is(err, ErrClosed) {
		t.Fatalf("Start after Shutdown error = %v, want ErrClosed", err)
	}
}

func TestManagerRejectsNamespaceAndContainerEscapes(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	signer, _ := ssh.NewSignerFromKey(privateKey)
	client := newFakeExecClient(t)
	now := time.Now().UTC()
	session := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     podSSHSessionActive,
		ExpiresAt: now.Add(time.Minute),
	}
	manager, err := New(client, &fakeSessions{sessions: map[string]remote.Session{"server-a": session}}, Config{
		ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)}, HostTCPRegistrar: &fakeHostTCPRegistrar{},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{ProfileID: "server-a", Namespace: "production", Pod: "api", PodIP: "10.0.0.1", Ready: true, Containers: []string{"main"}},
		{ProfileID: "server-a", Namespace: "development", Pod: "api", Container: "admin", PodIP: "10.0.0.1", Ready: true, Containers: []string{"main"}},
	} {
		if _, err := manager.Start(
			context.Background(),
			profile.Profile{ID: "server-a"},
			session,
			request,
		); err == nil {
			t.Fatalf("unsafe Pod SSH request accepted: %#v", request)
		}
	}
	if len(manager.List("")) != 0 {
		t.Fatalf("unsafe Pod SSH endpoints = %#v", manager.List(""))
	}
}

func TestManagerStopProfileWaitsForStartingEndpoint(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := remote.Session{
		ID: uuid.NewString(), Namespace: "development", State: podSSHSessionActive,
		ExpiresAt: now.Add(time.Minute),
	}
	registrar := &blockingHostTCPRegistrar{
		fakeHostTCPRegistrar: &fakeHostTCPRegistrar{},
		started:              make(chan struct{}),
		release:              make(chan struct{}),
	}
	t.Cleanup(registrar.unblock)
	manager, err := New(
		newFakeExecClient(t),
		&fakeSessions{sessions: map[string]remote.Session{"server-a": session}},
		Config{
			ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)}, HostTCPRegistrar: registrar,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan error, 1)
	go func() {
		_, startErr := manager.Start(t.Context(), profile.Profile{ID: "server-a"}, session, Request{
			ProfileID: "server-a", Namespace: "development", Pod: "api-0", PodIP: "10.244.1.7",
			Ready: true, Containers: []string{"main"},
		})
		started <- startErr
	}()
	select {
	case <-registrar.started:
	case <-time.After(time.Second):
		t.Fatal("Pod SSH endpoint did not reach Host TCP registration")
	}
	stopped := make(chan error, 1)
	go func() { stopped <- manager.StopProfile("server-a") }()
	select {
	case err := <-stopped:
		t.Fatalf("StopProfile bypassed an in-flight Start: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	registrar.unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-stopped; err != nil {
		t.Fatal(err)
	}
	if endpoints := manager.List("server-a"); len(endpoints) != 0 {
		t.Fatalf("endpoint committed after StopProfile: %#v", endpoints)
	}
}

func TestManagerStopProfilePreservesOtherProfiles(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessions := &fakeSessions{sessions: map[string]remote.Session{
		"server-a": {
			ID: uuid.NewString(), Namespace: "development", State: podSSHSessionActive,
			ExpiresAt: now.Add(time.Minute),
		},
		"server-b": {
			ID: uuid.NewString(), Namespace: "development", State: podSSHSessionActive,
			ExpiresAt: now.Add(time.Minute),
		},
	}}
	manager, err := New(newFakeExecClient(t), sessions, Config{
		ServerOptions:    []localpodssh.Option{localpodssh.WithSigner(signer)},
		HostTCPRegistrar: &fakeHostTCPRegistrar{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })

	for _, profileID := range []string{"server-a", "server-b"} {
		request := Request{
			ProfileID: profileID, Namespace: "development", Pod: "api-0-" + profileID,
			PodIP: "10.244.1.7", Ready: true, Containers: []string{"main"},
		}
		if _, err := manager.Start(
			t.Context(),
			profile.Profile{ID: profileID},
			sessions.sessions[profileID],
			request,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := manager.StopProfile("server-a"); err != nil {
		t.Fatal(err)
	}
	if items := manager.List("server-a"); len(items) != 0 {
		t.Fatalf("server-a endpoints=%#v, want none", items)
	}
	if items := manager.List("server-b"); len(items) != 1 || items[0].ProfileID != "server-b" {
		t.Fatalf("server-b endpoints=%#v, want one preserved endpoint", items)
	}
}

func TestManagerIsolatesEndpointByLocalUserKey(t *testing.T) {
	_, privateKeyA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signerA, err := ssh.NewSignerFromKey(privateKeyA)
	if err != nil {
		t.Fatal(err)
	}
	_, privateKeyB, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signerB, err := ssh.NewSignerFromKey(privateKeyB)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	sessionA := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     podSSHSessionActive,
		ExpiresAt: now.Add(time.Minute),
	}
	sessionB := remote.Session{
		ID:        uuid.NewString(),
		Namespace: "development",
		State:     podSSHSessionActive,
		ExpiresAt: now.Add(time.Minute),
	}
	sessions := &fakeSessions{sessions: map[string]remote.Session{
		"server-a": sessionA,
		"server-b": sessionB,
	}}
	client := newFakeExecClient(t)
	registrar := &fakeHostTCPRegistrar{}
	managerA, err := New(client, sessions, Config{
		ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerA)}, HostTCPRegistrar: registrar,
	})
	if err != nil {
		t.Fatal(err)
	}
	managerB, err := New(client, sessions, Config{
		ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerB)}, HostTCPRegistrar: registrar,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = managerA.Shutdown()
		_ = managerB.Shutdown()
	})
	request := Request{
		Namespace: "development", Pod: "api-0", Container: "main",
		PodIP: "10.244.1.7", Ready: true, Containers: []string{"main"},
	}
	request.ProfileID = "server-a"
	endpointA, err := managerA.Start(context.Background(), profile.Profile{ID: "server-a"}, sessionA, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ProfileID = "server-b"
	endpointB, err := managerB.Start(context.Background(), profile.Profile{ID: "server-b"}, sessionB, request)
	if err != nil {
		t.Fatal(err)
	}
	if endpointA.Address != endpointB.Address || endpointA.Address != "10.244.1.7:22" {
		t.Fatalf("native endpoints = %q and %q", endpointA.Address, endpointB.Address)
	}

	unauthorizedRaw, err := registrar.dial()
	if err != nil {
		t.Fatal(err)
	}
	unauthorizedConnection, unauthorizedChannels, unauthorizedRequests, err := ssh.NewClientConn(
		unauthorizedRaw,
		endpointA.Address,
		&ssh.ClientConfig{
			User: "main", Auth: []ssh.AuthMethod{ssh.PublicKeys(signerB)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second,
		},
	)
	if err == nil {
		_ = ssh.NewClient(unauthorizedConnection, unauthorizedChannels, unauthorizedRequests).Close()
		t.Fatal("user B authenticated to user A Pod SSH endpoint")
	}
	authorizedRaw, err := registrar.dial()
	if err != nil {
		t.Fatal(err)
	}
	authorizedConnection, authorizedChannels, authorizedRequests, err := ssh.NewClientConn(
		authorizedRaw,
		endpointA.Address,
		&ssh.ClientConfig{
			User: "main", Auth: []ssh.AuthMethod{ssh.PublicKeys(signerA)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("user A could not authenticate to own Pod SSH endpoint: %v", err)
	}
	_ = ssh.NewClient(authorizedConnection, authorizedChannels, authorizedRequests).Close()
}
