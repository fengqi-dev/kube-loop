package podssh

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
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
