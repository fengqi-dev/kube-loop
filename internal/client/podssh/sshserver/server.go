package sshserver

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
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
	return func(server *Server) {
		server.hostKeyPath = path
	}
}

// Server adapts SSH sessions addressed to Pod IPs into Kubernetes exec streams.
type Server struct {
	executor Executor

	mu             sync.RWMutex
	targets        map[string]Target
	connections    map[net.Conn]string
	connectionDone map[net.Conn]<-chan struct{}
	hostSigner     ssh.Signer
	hostKeyPath    string
	signerOnce     sync.Once
	signerErr      error

	clientKeys         []ssh.PublicKey
	clientIdentityPath string
	authOnce           sync.Once
	authErr            error
}

func NewServer(executor Executor, options ...Option) *Server {
	server := &Server{
		executor:       executor,
		targets:        make(map[string]Target),
		connections:    make(map[net.Conn]string),
		connectionDone: make(map[net.Conn]<-chan struct{}),
	}
	for _, option := range options {
		option(server)
	}
	return server
}

// Enable claims target.IP:22 on the host TUN path.
func (s *Server) Enable(target Target) (Info, error) {
	if s == nil || s.executor == nil {
		return Info{}, errors.New("pod SSH is unavailable")
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
	s.targets[targetID(target)] = target
	s.mu.Unlock()
	return s.info(target), nil
}
