package podssh

import (
	"context"
	"crypto/ed25519"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

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
		connection, err := websocket.Accept(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.CloseNow()
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		for {
			_, encoded, err := connection.Read(ctx)
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
			if err := connection.Write(ctx, websocket.MessageBinary, stdout); err != nil {
				return
			}
			exit, _ := execstream.EncodeExit(execstream.ExitStatus{})
			if err := connection.Write(ctx, websocket.MessageBinary, exit); err != nil {
				return
			}
			_ = connection.Close(websocket.StatusNormalClosure, "exec complete")
			return
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
	connection, _, err := websocket.Dial(ctx, endpoint, nil)
	return connection, err
}

func TestManagerServesLoopbackSSHThroughRemoteExec(t *testing.T) {
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
		ID: uuid.NewString(), Namespace: "development", State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(time.Minute),
	}
	sessions := &fakeSessions{sessions: map[string]remote.Session{"server-a": session}}
	manager, err := New(client, sessions, Config{ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown() })
	info, err := manager.Start(context.Background(), profile.Profile{ID: "server-a", BaseURL: "https://gateway.test"}, session, Request{
		ProfileID: "server-a", Namespace: "development", Pod: "api-0", Container: "main",
		PodIP: "10.244.1.7", Ready: true, Containers: []string{"main", "sidecar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(info.Address, "127.0.0.1:") || info.Port == 0 || !strings.Contains(info.Command, "-p") || !strings.Contains(info.Command, "main@127.0.0.1") {
		t.Fatalf("Pod SSH info = %#v", info)
	}
	raw, err := net.DialTimeout("tcp", info.Address, time.Second)
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
	defer sshClient.Close()
	sshSession, err := sshClient.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sshSession.Close()
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
	listener, err := net.Listen("tcp", info.Address)
	if err != nil {
		t.Fatalf("Pod SSH loopback listener was not released: %v", err)
	}
	_ = listener.Close()
}

func TestManagerRejectsNamespaceAndContainerEscapes(t *testing.T) {
	_, privateKey, _ := ed25519.GenerateKey(nil)
	signer, _ := ssh.NewSignerFromKey(privateKey)
	client := newFakeExecClient(t)
	now := time.Now().UTC()
	session := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Minute)}
	manager, err := New(client, &fakeSessions{sessions: map[string]remote.Session{"server-a": session}}, Config{
		ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signer)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []Request{
		{ProfileID: "server-a", Namespace: "production", Pod: "api", PodIP: "10.0.0.1", Ready: true, Containers: []string{"main"}},
		{ProfileID: "server-a", Namespace: "development", Pod: "api", Container: "admin", PodIP: "10.0.0.1", Ready: true, Containers: []string{"main"}},
	} {
		if _, err := manager.Start(context.Background(), profile.Profile{ID: "server-a"}, session, request); err == nil {
			t.Fatalf("unsafe Pod SSH request accepted: %#v", request)
		}
	}
	if len(manager.List("")) != 0 {
		t.Fatalf("unsafe Pod SSH endpoints = %#v", manager.List(""))
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
	sessionA := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Minute)}
	sessionB := remote.Session{ID: uuid.NewString(), Namespace: "development", State: "active", ExpiresAt: now.Add(time.Minute)}
	sessions := &fakeSessions{sessions: map[string]remote.Session{
		"server-a": sessionA,
		"server-b": sessionB,
	}}
	client := newFakeExecClient(t)
	managerA, err := New(client, sessions, Config{ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerA)}})
	if err != nil {
		t.Fatal(err)
	}
	managerB, err := New(client, sessions, Config{ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerB)}})
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
	if endpointA.Address == endpointB.Address {
		t.Fatalf("isolated endpoints unexpectedly share address %q", endpointA.Address)
	}

	unauthorized, err := ssh.Dial("tcp", endpointA.Address, &ssh.ClientConfig{
		User: "main", Auth: []ssh.AuthMethod{ssh.PublicKeys(signerB)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second,
	})
	if err == nil {
		_ = unauthorized.Close()
		t.Fatal("user B authenticated to user A Pod SSH endpoint")
	}
	authorized, err := ssh.Dial("tcp", endpointA.Address, &ssh.ClientConfig{
		User: "main", Auth: []ssh.AuthMethod{ssh.PublicKeys(signerA)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("user A could not authenticate to own Pod SSH endpoint: %v", err)
	}
	_ = authorized.Close()
}
