package podssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"k8s.io/client-go/tools/remotecommand"
)

type Option func(*Server)

func WithSigner(signer ssh.Signer) Option {
	return func(server *Server) {
		server.hostSigner = signer
		server.clientKeys = []ssh.PublicKey{signer.PublicKey()}
	}
}

func WithClientIdentityPath(path string) Option {
	return func(server *Server) { server.clientIdentityPath = path }
}

func WithHostKeyPath(path string) Option {
	return func(server *Server) { server.hostKeyPath = path }
}

// Server adapts SSH sessions addressed to Pod IPs into Kubernetes exec streams.
type Server struct {
	executor Executor

	mu          sync.RWMutex
	targets     map[string]Target
	connections map[net.Conn]string
	hostSigner  ssh.Signer
	hostKeyPath string
	signerOnce  sync.Once
	signerErr   error

	clientKeys         []ssh.PublicKey
	clientIdentityPath string
	authOnce           sync.Once
	authErr            error
}

func NewServer(executor Executor, options ...Option) *Server {
	server := &Server{
		executor:    executor,
		targets:     make(map[string]Target),
		connections: make(map[net.Conn]string),
	}
	for _, option := range options {
		option(server)
	}
	return server
}

// Enable claims target.IP:22 on the host TUN path.
func (s *Server) Enable(target Target) (Info, error) {
	if s == nil || s.executor == nil {
		return Info{}, errors.New("Pod SSH is unavailable")
	}
	if target.Context == "" || target.Namespace == "" || target.Pod == "" || target.Container == "" {
		return Info{}, errors.New("context, namespace, pod, and container are required")
	}
	if len(target.Containers) == 0 {
		target.Containers = []string{target.Container}
	}
	if !contains(target.Containers, target.Container) {
		return Info{}, fmt.Errorf("container %q is not available for Pod SSH", target.Container)
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(target.IP))
	if err != nil {
		return Info{}, fmt.Errorf("invalid Pod IP %q: %w", target.IP, err)
	}
	target.IP = ip.Unmap().String()
	if _, err := s.signer(); err != nil {
		return Info{}, err
	}
	if _, err := s.authorizedClientKeys(); err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	s.targets[target.IP] = target
	s.mu.Unlock()
	return s.info(target), nil
}

func (s *Server) Disable(id string) error {
	s.mu.Lock()
	found := false
	for ip, target := range s.targets {
		if targetID(target) == id {
			delete(s.targets, ip)
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("Pod SSH endpoint %q not found", id)
	}
	connections := make([]net.Conn, 0)
	for connection, target := range s.connections {
		if target == id {
			connections = append(connections, connection)
		}
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
	return nil
}

func (s *Server) List() []Info {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	items := make([]Info, 0, len(s.targets))
	for _, target := range s.targets {
		items = append(items, s.info(target))
	}
	s.mu.RUnlock()
	sort.Slice(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Pod < items[j].Pod
	})
	return items
}

// Reconcile exposes every supplied Pod by default, follows replacement IPs,
// and drops endpoints whose Pod/container disappeared.
func (s *Server) Reconcile(pods []PodRef) error {
	if s == nil {
		return nil
	}
	live := make(map[string]PodRef, len(pods))
	for _, pod := range pods {
		ip, err := netip.ParseAddr(strings.TrimSpace(pod.IP))
		if err != nil || len(pod.Containers) == 0 {
			continue
		}
		pod.IP = ip.Unmap().String()
		live[podIdentity(pod.Context, pod.Namespace, pod.Pod)] = pod
	}
	if len(live) > 0 {
		if _, err := s.signer(); err != nil {
			return err
		}
		if _, err := s.authorizedClientKeys(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	current := make(map[string]Target, len(s.targets))
	for _, target := range s.targets {
		current[targetID(target)] = target
	}
	next := make(map[string]Target, len(live))
	activeIDs := make(map[string]struct{}, len(live))
	for id, pod := range live {
		container := pod.Containers[0]
		if target, ok := current[id]; ok && contains(pod.Containers, target.Container) {
			container = target.Container
		}
		target := Target{
			Context: pod.Context, Namespace: pod.Namespace, Pod: pod.Pod,
			Container: container, Containers: append([]string{}, pod.Containers...), IP: pod.IP,
		}
		next[target.IP] = target
		activeIDs[id] = struct{}{}
	}
	s.targets = next
	connections := make([]net.Conn, 0)
	for connection, id := range s.connections {
		if _, ok := activeIDs[id]; !ok {
			connections = append(connections, connection)
		}
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
	return nil
}

// Reset disables every endpoint and terminates active SSH connections.
func (s *Server) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.targets = make(map[string]Target)
	connections := make([]net.Conn, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
	}
	s.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

// HostTCP is installed before the normal Service intercept handler.
func (s *Server) HostTCP(host string, port uint16) (func(net.Conn), bool) {
	if s == nil || port != DefaultPort {
		return nil, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return nil, false
	}
	s.mu.RLock()
	target, ok := s.targets[ip.Unmap().String()]
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return func(connection net.Conn) {
		s.serveConnection(connection, target)
	}, true
}

func (s *Server) serveConnection(raw net.Conn, target Target) {
	s.mu.Lock()
	s.connections[raw] = targetID(target)
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.connections, raw)
		s.mu.Unlock()
		_ = raw.Close()
	}()

	signer, err := s.signer()
	if err != nil {
		return
	}
	authorizedKeys, err := s.authorizedClientKeys()
	if err != nil {
		return
	}
	config := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-KubeLoop",
		PublicKeyCallback: func(
			metadata ssh.ConnMetadata,
			key ssh.PublicKey,
		) (*ssh.Permissions, error) {
			if _, ok := targetForLogin(target, metadata.User()); !ok {
				return nil, fmt.Errorf(
					"unknown container %q in Pod %s/%s",
					metadata.User(), target.Namespace, target.Pod,
				)
			}
			for _, authorizedKey := range authorizedKeys {
				if bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("unknown Pod SSH client key")
		},
	}
	config.AddHostKey(signer)
	connection, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		return
	}
	defer connection.Close()
	selectedTarget, ok := targetForLogin(target, connection.User())
	if !ok {
		return
	}
	go ssh.DiscardRequests(requests)
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		go s.serveSession(channel, channelRequests, selectedTarget)
	}
}

type sessionState struct {
	ctx       context.Context
	cancel    context.CancelFunc
	channel   ssh.Channel
	target    Target
	executor  Executor
	terminal  *terminalSizeQueue
	tty       bool
	startOnce sync.Once
}

func (s *Server) serveSession(
	channel ssh.Channel,
	requests <-chan *ssh.Request,
	target Target,
) {
	ctx, cancel := context.WithCancel(context.Background())
	state := &sessionState{
		ctx: ctx, cancel: cancel, channel: channel, target: target, executor: s.executor,
		terminal: newTerminalSizeQueue(),
	}
	defer func() {
		cancel()
		state.terminal.Close()
		_ = channel.Close()
	}()
	for request := range requests {
		switch request.Type {
		case "pty-req":
			var payload ptyRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			state.tty = true
			state.terminal.Push(payload.Columns, payload.Rows)
			_ = request.Reply(true, nil)
		case "window-change":
			var payload windowChangeRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err == nil {
				state.terminal.Push(payload.Columns, payload.Rows)
			}
		case "env":
			// Kubernetes exec inherits the container environment. Accepting the
			// request keeps common SSH clients compatible without mutating it.
			_ = request.Reply(true, nil)
		case "shell":
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runExec([]string{"/bin/sh"})
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "exec":
			var payload execRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
				_ = request.Reply(false, nil)
				continue
			}
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runExec([]string{"/bin/sh", "-c", payload.Command})
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "subsystem":
			var payload subsystemRequest
			if err := ssh.Unmarshal(request.Payload, &payload); err != nil || payload.Name != "sftp" {
				_ = request.Reply(false, nil)
				continue
			}
			started := false
			state.startOnce.Do(func() {
				started = true
				_ = request.Reply(true, nil)
				go state.runSFTP()
			})
			if !started {
				_ = request.Reply(false, nil)
			}
		case "signal":
			cancel()
			_ = request.Reply(true, nil)
		default:
			_ = request.Reply(false, nil)
		}
	}
}

func (s *sessionState) runExec(command []string) {
	streams := Streams{
		Stdin:  s.channel,
		Stdout: s.channel,
		Stderr: s.channel.Stderr(),
		TTY:    s.tty,
	}
	if s.tty {
		streams.Stderr = nil
		streams.TerminalSizeQueue = s.terminal
	}
	err := s.executor.Exec(s.ctx, s.target, command, streams)
	status := uint32(0)
	if err != nil {
		status = 1
		_, _ = fmt.Fprintln(s.channel.Stderr(), err)
	}
	_, _ = s.channel.SendRequest("exit-status", false, ssh.Marshal(exitStatus{Status: status}))
	_ = s.channel.Close()
	s.cancel()
}

func (s *sessionState) runSFTP() {
	handler := newSFTPHandler(s.executor, s.target)
	server := sftp.NewRequestServer(s.channel, sftp.Handlers{
		FileGet: handler, FilePut: handler, FileCmd: handler, FileList: handler,
	}, sftp.WithStartDirectory("/"))
	err := server.Serve()
	status := uint32(0)
	if err != nil && !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
		status = 1
	}
	_, _ = s.channel.SendRequest("exit-status", false, ssh.Marshal(exitStatus{Status: status}))
	_ = server.Close()
	_ = s.channel.Close()
	s.cancel()
}

type ptyRequest struct {
	Term          string
	Columns, Rows uint32
	Width, Height uint32
	TerminalModes string
}

type windowChangeRequest struct {
	Columns, Rows uint32
	Width, Height uint32
}

type execRequest struct {
	Command string
}

type subsystemRequest struct {
	Name string
}

type exitStatus struct {
	Status uint32
}

type terminalSizeQueue struct {
	sizes chan remotecommand.TerminalSize
	done  chan struct{}
	once  sync.Once
}

func newTerminalSizeQueue() *terminalSizeQueue {
	return &terminalSizeQueue{
		sizes: make(chan remotecommand.TerminalSize, 1),
		done:  make(chan struct{}),
	}
}

func (q *terminalSizeQueue) Push(width, height uint32) {
	size := remotecommand.TerminalSize{Width: uint16(width), Height: uint16(height)}
	select {
	case <-q.done:
		return
	default:
	}
	select {
	case q.sizes <- size:
	default:
		select {
		case <-q.sizes:
		default:
		}
		select {
		case q.sizes <- size:
		default:
		}
	}
}

func (q *terminalSizeQueue) Next() *remotecommand.TerminalSize {
	select {
	case size := <-q.sizes:
		return &size
	case <-q.done:
		return nil
	}
}

func (q *terminalSizeQueue) Close() {
	q.once.Do(func() { close(q.done) })
}

func (s *Server) signer() (ssh.Signer, error) {
	s.signerOnce.Do(func() {
		if s.hostSigner != nil {
			return
		}
		path := s.hostKeyPath
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				s.signerErr = fmt.Errorf("find home directory for SSH host key: %w", err)
				return
			}
			path = filepath.Join(home, ".kubeloop", "ssh_host_ed25519")
		}
		s.hostSigner, s.signerErr = loadOrCreateSigner(path)
	})
	return s.hostSigner, s.signerErr
}

func (s *Server) authorizedClientKeys() ([]ssh.PublicKey, error) {
	s.authOnce.Do(func() {
		if len(s.clientKeys) > 0 {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			s.authErr = fmt.Errorf("find home directory for Pod SSH identity: %w", err)
			return
		}
		s.clientKeys, s.clientIdentityPath, s.authErr = loadOrCreateUserSSHKeys(home)
	})
	if s.authErr != nil {
		return nil, s.authErr
	}
	if len(s.clientKeys) == 0 {
		return nil, errors.New("Pod SSH client identity is unavailable")
	}
	return append([]ssh.PublicKey{}, s.clientKeys...), nil
}

func loadOrCreateUserSSHKeys(home string) ([]ssh.PublicKey, string, error) {
	sshDir := filepath.Join(home, ".ssh")
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa"}
	keys := make([]ssh.PublicKey, 0, len(names))
	identityPath := ""
	var firstErr error
	ed25519Occupied := false
	for _, name := range names {
		privatePath := filepath.Join(sshDir, name)
		publicPath := privatePath + ".pub"
		if name == "id_ed25519" {
			_, privateErr := os.Stat(privatePath)
			_, publicErr := os.Stat(publicPath)
			ed25519Occupied = privateErr == nil || publicErr == nil
		}
		var publicKey ssh.PublicKey
		if content, err := os.ReadFile(publicPath); err == nil {
			key, _, _, _, parseErr := ssh.ParseAuthorizedKey(content)
			if parseErr == nil {
				publicKey = key
			} else if firstErr == nil {
				firstErr = fmt.Errorf("parse user SSH public key %s: %w", publicPath, parseErr)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("read user SSH public key %s: %w", publicPath, err)
		}
		if content, err := os.ReadFile(privatePath); err == nil {
			signer, parseErr := ssh.ParsePrivateKey(content)
			if parseErr == nil {
				keys = append(keys, signer.PublicKey())
				if identityPath == "" {
					identityPath = privatePath
				}
				continue
			}
			if publicKey != nil {
				keys = append(keys, publicKey)
				if identityPath == "" {
					identityPath = privatePath
				}
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"parse user SSH private key %s (create its .pub file if it is encrypted): %w",
					privatePath, parseErr,
				)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("read user SSH private key %s: %w", privatePath, err)
		}
		if publicKey != nil {
			keys = append(keys, publicKey)
		}
	}
	if len(keys) > 0 {
		return keys, identityPath, nil
	}
	if ed25519Occupied {
		if firstErr != nil {
			return nil, "", firstErr
		}
		return nil, "", errors.New("~/.ssh/id_ed25519 exists but no usable public key was found")
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create user SSH directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate user SSH identity: %w", err)
	}
	privatePath := filepath.Join(sshDir, "id_ed25519")
	if err := writeNewOpenSSHPrivateKey(privatePath, privateKey); err != nil {
		return nil, "", fmt.Errorf("write user SSH identity: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		_ = os.Remove(privatePath)
		return nil, "", fmt.Errorf("create user SSH signer: %w", err)
	}
	publicPath := privatePath + ".pub"
	if err := writeNewFile(publicPath, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
		_ = os.Remove(privatePath)
		return nil, "", fmt.Errorf("write user SSH public key: %w", err)
	}
	return []ssh.PublicKey{signer.PublicKey()}, privatePath, nil
}

func loadOrCreateSigner(path string) (ssh.Signer, error) {
	if content, err := os.ReadFile(path); err == nil {
		privateKey, parseErr := ssh.ParseRawPrivateKey(content)
		if parseErr != nil {
			return nil, fmt.Errorf("parse Pod SSH key: %w", parseErr)
		}
		signer, parseErr := ssh.NewSignerFromKey(privateKey)
		if parseErr != nil {
			return nil, fmt.Errorf("create Pod SSH signer: %w", parseErr)
		}
		block, _ := pem.Decode(content)
		if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
			if err := writeOpenSSHPrivateKey(path, privateKey); err != nil {
				return nil, fmt.Errorf("migrate Pod SSH key to OpenSSH format: %w", err)
			}
		} else if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure Pod SSH key permissions: %w", err)
		}
		return signer, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read Pod SSH key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Pod SSH key directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Pod SSH key: %w", err)
	}
	if err := writeOpenSSHPrivateKey(path, privateKey); err != nil {
		return nil, fmt.Errorf("write Pod SSH key: %w", err)
	}
	return ssh.NewSignerFromKey(privateKey)
}

func writeNewOpenSSHPrivateKey(path string, privateKey any) error {
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop Pod SSH")
	if err != nil {
		return fmt.Errorf("encode OpenSSH private key: %w", err)
	}
	return writeNewFile(path, pem.EncodeToMemory(block), 0o600)
}

func writeNewFile(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeOpenSSHPrivateKey(path string, privateKey any) error {
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop Pod SSH")
	if err != nil {
		return fmt.Errorf("encode OpenSSH private key: %w", err)
	}
	content := pem.EncodeToMemory(block)
	temp, err := os.CreateTemp(filepath.Dir(path), ".pod_ssh_key-*")
	if err != nil {
		return fmt.Errorf("create temporary key: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("install key: %w", err)
	}
	return nil
}

func (s *Server) info(target Target) Info {
	command := "ssh "
	if s.clientIdentityPath != "" {
		command += "-i " + shellQuote(s.clientIdentityPath) + " "
	}
	command += fmt.Sprintf("%s@%s", target.Container, target.IP)
	return Info{
		ID: targetID(target), Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container, IP: target.IP, Port: DefaultPort,
		Command: command,
	}
}

func targetForLogin(target Target, login string) (Target, bool) {
	containers := target.Containers
	if len(containers) == 0 {
		containers = []string{target.Container}
	}
	if !contains(containers, login) {
		return Target{}, false
	}
	target.Container = login
	return target, true
}

func targetID(target Target) string {
	return podIdentity(target.Context, target.Namespace, target.Pod)
}

func podIdentity(contextName, namespace, pod string) string {
	return contextName + "/" + namespace + "/" + pod
}

func contains(items []string, wanted string) bool {
	for _, item := range items {
		if item == wanted {
			return true
		}
	}
	return false
}
