package listener

import (
	"context"
	"fmt"
	"maps"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	KindPod     = "pod"
	KindService = "service"
)

// Request starts a local API Server port-forward to a Pod or Service.
type Request struct {
	Context    string `json:"context"`
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"` // pod | service
	Name       string `json:"name"`
	Protocol   string `json:"protocol,omitempty"` // tcp | udp
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort"` // 0 = allocate
}

// Info describes an active port-forward.
type Info struct {
	ID         string `json:"id"`
	Context    string `json:"context"`
	Namespace  string `json:"namespace"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	PodName    string `json:"podName"`
	Protocol   string `json:"protocol"`
	RemotePort uint16 `json:"remotePort"`
	LocalPort  uint16 `json:"localPort"`
	Address    string `json:"address"`
}

type Forwarder interface {
	Address() string
	Close() error
}

type TrafficDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type Manager struct {
	mu     sync.Mutex
	nextID atomic.Uint64
	active map[string]*runtimeForward
}

type runtimeForward struct {
	info      Info
	forwarder Forwarder
}

func NewManager() *Manager {
	return &Manager{active: make(map[string]*runtimeForward)}
}

func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	items := make([]Info, 0, len(m.active))
	for _, item := range m.active {
		items = append(items, item.info)
	}
	return items
}

// StartResolved starts only the loopback listener. The target must already be
// resolved by a trusted Gateway, so this path never reads kubeconfig or calls a
// Kubernetes API from the desktop.
func (m *Manager) StartResolved(
	ctx context.Context,
	request Request,
	target string,
	dialer TrafficDialer,
) (Info, error) {
	if dialer == nil {
		return Info{}, fmt.Errorf("port Forward traffic dialer is required")
	}
	if request.Context == "" {
		return Info{}, fmt.Errorf("context is required")
	}
	if request.Namespace == "" {
		return Info{}, fmt.Errorf("namespace is required")
	}
	if request.Name == "" {
		return Info{}, fmt.Errorf("target name is required")
	}
	if request.RemotePort == 0 {
		return Info{}, fmt.Errorf("remote port is required")
	}
	request.Kind = strings.ToLower(strings.TrimSpace(request.Kind))
	if request.Kind != KindPod && request.Kind != KindService {
		return Info{}, fmt.Errorf("unsupported kind %q", request.Kind)
	}
	request.Protocol = strings.ToLower(strings.TrimSpace(request.Protocol))
	if request.Protocol == "" {
		request.Protocol = "tcp"
	}
	if request.Protocol != "tcp" && request.Protocol != "udp" {
		return Info{}, fmt.Errorf("unsupported protocol %q", request.Protocol)
	}
	host, rawPort, err := net.SplitHostPort(strings.TrimSpace(target))
	if err != nil || host == "" || rawPort == "" {
		return Info{}, fmt.Errorf("resolved Port Forward target is invalid")
	}
	return m.startRouted(ctx, request, target, dialer)
}

func (m *Manager) startRouted(
	ctx context.Context,
	request Request,
	target string,
	dialer TrafficDialer,
) (Info, error) {
	listenAddress := net.JoinHostPort("127.0.0.1", strconv.Itoa(int(request.LocalPort)))
	var forwarder Forwarder
	if request.Protocol == "udp" {
		address, err := net.ResolveUDPAddr("udp", listenAddress)
		if err != nil {
			return Info{}, fmt.Errorf("resolve UDP port-forward listener: %w", err)
		}
		socket, err := net.ListenUDP("udp", address)
		if err != nil {
			return Info{}, fmt.Errorf("listen for UDP port-forward: %w", err)
		}
		forwarder = newRoutedUDPForwarder(socket, target, dialer)
	} else {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listenAddress)
		if err != nil {
			return Info{}, fmt.Errorf("listen for port-forward: %w", err)
		}
		forwarder = newRoutedForwarder(listener, target, dialer)
	}
	localPort, err := localPortFromAddress(forwarder.Address())
	if err != nil {
		_ = forwarder.Close()
		return Info{}, err
	}
	id := fmt.Sprintf("pf-%d", m.nextID.Add(1))
	info := Info{
		ID:         id,
		Context:    request.Context,
		Namespace:  request.Namespace,
		Kind:       request.Kind,
		Name:       request.Name,
		Protocol:   request.Protocol,
		RemotePort: request.RemotePort,
		LocalPort:  localPort,
		Address:    forwarder.Address(),
	}
	m.mu.Lock()
	m.active[id] = &runtimeForward{info: info, forwarder: forwarder}
	m.mu.Unlock()
	return info, nil
}

func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	runtime := m.active[id]
	delete(m.active, id)
	m.mu.Unlock()
	if runtime == nil {
		return fmt.Errorf("port-forward %q not found", id)
	}
	return runtime.forwarder.Close()
}

func (m *Manager) StopAll() {
	m.mu.Lock()
	items := slices.Collect(maps.Values(m.active))
	clear(m.active)
	m.mu.Unlock()
	for _, item := range items {
		_ = item.forwarder.Close()
	}
}

func localPortFromAddress(address string) (uint16, error) {
	_, portText, err := net.SplitHostPort(address)
	if err != nil {
		return 0, fmt.Errorf("parse forward address: %w", err)
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return 0, fmt.Errorf("invalid local port %q", portText)
	}
	return uint16(port), nil
}
