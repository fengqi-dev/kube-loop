package mirror

import (
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
)

type Client interface {
	CreateMirror(context.Context, profile.Profile, remote.Session, remote.MirrorSpec, string) (remote.MirrorTask, error)
	StopMirror(context.Context, profile.Profile, remote.Session, string) (remote.MirrorTask, error)
}

type TrafficStreamOpener interface {
	OpenTrafficStream(context.Context, string, string, string) (*trafficstream.FrameConn, error)
}

type LocalTarget struct {
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
	LocalHost   string `json:"localHost"`
	LocalPort   uint16 `json:"localPort"`
}

type Request struct {
	ProfileID string        `json:"profileId"`
	Service   string        `json:"service"`
	Targets   []LocalTarget `json:"targets"`
}

type Info struct {
	ID        string        `json:"id"`
	ProfileID string        `json:"profileId"`
	SessionID string        `json:"sessionId"`
	Namespace string        `json:"namespace"`
	Service   string        `json:"service"`
	ClusterIP string        `json:"clusterIp"`
	State     string        `json:"state"`
	Targets   []LocalTarget `json:"targets"`
}

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type Config struct {
	DialContext        DialContextFunc
	ShadowQueueSize    int
	ShadowDialTimeout  time.Duration
	ShadowWriteTimeout time.Duration
	ShadowIdleTimeout  time.Duration
	TrafficStreams     TrafficStreamOpener
}

const (
	defaultShadowQueueSize    = 16
	defaultShadowDialTimeout  = 2 * time.Second
	defaultShadowWriteTimeout = 500 * time.Millisecond
	defaultShadowIdleTimeout  = 30 * time.Second
)

type activeMirror struct {
	profile profile.Profile
	session remote.Session
	task    remote.MirrorTask
	relay   *localRelay
	cancel  context.CancelFunc
	done    chan struct{}
	info    Info
}

type Manager struct {
	client  Client
	streams TrafficStreamOpener
	dial    DialContextFunc
	config  Config

	mu     sync.Mutex
	active map[string]*activeMirror
}

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil || config.TrafficStreams == nil {
		return nil, errors.New("mirror control client and Data Plane stream opener are required")
	}
	streams := config.TrafficStreams
	config.TrafficStreams = nil
	if config.DialContext == nil {
		dialer := &net.Dialer{}
		config.DialContext = dialer.DialContext
	}
	if config.ShadowQueueSize == 0 {
		config.ShadowQueueSize = defaultShadowQueueSize
	}
	if config.ShadowDialTimeout == 0 {
		config.ShadowDialTimeout = defaultShadowDialTimeout
	}
	if config.ShadowWriteTimeout == 0 {
		config.ShadowWriteTimeout = defaultShadowWriteTimeout
	}
	if config.ShadowIdleTimeout == 0 {
		config.ShadowIdleTimeout = defaultShadowIdleTimeout
	}
	if config.ShadowQueueSize < 1 || config.ShadowQueueSize > 1024 ||
		config.ShadowDialTimeout < 100*time.Millisecond || config.ShadowDialTimeout > time.Minute ||
		config.ShadowWriteTimeout < 10*time.Millisecond || config.ShadowWriteTimeout > time.Minute ||
		config.ShadowIdleTimeout < 100*time.Millisecond || config.ShadowIdleTimeout > 24*time.Hour {
		return nil, errors.New("mirror shadow queue or timeout configuration is invalid")
	}
	return &Manager{
		client: client, streams: streams, dial: config.DialContext,
		config: config, active: make(map[string]*activeMirror),
	}, nil
}

func (manager *Manager) Start(
	ctx context.Context,
	serverProfile profile.Profile,
	session remote.Session,
	request Request,
) (Info, error) {
	if ctx == nil || strings.TrimSpace(request.ProfileID) != serverProfile.ID || session.State != mirrorSessionActive {
		return Info{}, errors.New("active Server Profile Session is required")
	}
	targets, ports, err := normalizeTargets(request.Targets)
	if err != nil {
		return Info{}, err
	}
	task, err := manager.client.CreateMirror(ctx, serverProfile, session, remote.MirrorSpec{
		Service: strings.TrimSpace(request.Service), Ports: ports,
	}, "mirror:"+uuid.NewString())
	if err != nil {
		return Info{}, err
	}
	if err := matchTaskTargets(task, targets); err != nil {
		_, stopErr := manager.client.StopMirror(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	connection, err := manager.streams.OpenTrafficStream(ctx, serverProfile.ID, tunnel.TrafficModeMirror, task.ID)
	if err != nil || connection == nil {
		if err == nil {
			err = errors.New("data Plane returned an empty Mirror stream")
		}
		_, stopErr := manager.client.StopMirror(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	relay := newLocalRelay(connection, targets, manager.dial, manager.config)
	if err := relay.readReady(ctx); err != nil {
		_ = connection.Close()
		_, stopErr := manager.client.StopMirror(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(err, stopErr)
	}
	runContext, cancel := context.WithCancel(context.Background())
	entry := &activeMirror{
		profile: serverProfile, session: session, task: task, relay: relay, cancel: cancel, done: make(chan struct{}),
		info: Info{
			ID:        task.ID,
			ProfileID: serverProfile.ID,
			SessionID: session.ID,
			Namespace: session.Namespace,
			Service:   task.Service,
			ClusterIP: task.ClusterIP,
			State:     "running",
			Targets:   append([]LocalTarget(nil), targets...),
		},
	}
	manager.mu.Lock()
	if _, exists := manager.active[task.ID]; exists {
		manager.mu.Unlock()
		cancel()
		_ = connection.Close()
		_, stopErr := manager.client.StopMirror(ctx, serverProfile, session, task.ID)
		return Info{}, errors.Join(errors.New("mirror Task is already active locally"), stopErr)
	}
	manager.active[task.ID] = entry
	manager.mu.Unlock()
	go manager.run(runContext, entry)
	return entry.info, nil
}

func (manager *Manager) Stop(ctx context.Context, profileID, taskID string) error {
	if ctx == nil {
		return errors.New("mirror stop context is required")
	}
	manager.mu.Lock()
	entry := manager.active[taskID]
	if entry == nil || entry.profile.ID != profileID {
		entry = nil
	} else {
		delete(manager.active, taskID)
	}
	manager.mu.Unlock()
	if entry == nil {
		return errors.New("mirror is not active locally")
	}
	// Persist the stop request before notifying the stream owner. Sending the
	// stream frame first lets a fast owner race the DELETE state transition and
	// turn an otherwise idempotent stop into a 409 conflict.
	_, remoteErr := manager.client.StopMirror(ctx, entry.profile, entry.session, entry.task.ID)
	streamErr := entry.relay.stop(ctx)
	entry.cancel()
	select {
	case <-entry.done:
	case <-ctx.Done():
		streamErr = errors.Join(streamErr, ctx.Err())
	}
	return errors.Join(remoteErr, streamErr)
}

func (manager *Manager) List(profileID string) []Info {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	items := make([]Info, 0, len(manager.active))
	for _, entry := range manager.active {
		if profileID == "" || entry.profile.ID == profileID {
			item := entry.info
			item.Targets = append([]LocalTarget(nil), item.Targets...)
			items = append(items, item)
		}
	}
	slices.SortFunc(items, func(left, right Info) int { return strings.Compare(left.ID, right.ID) })
	return items
}

func (manager *Manager) StopProfile(ctx context.Context, profileID string) error {
	manager.mu.Lock()
	ids := make([]string, 0)
	for id, entry := range manager.active {
		if entry.profile.ID == profileID {
			ids = append(ids, id)
		}
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profileID, id))
	}
	return result
}

func (manager *Manager) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("mirror shutdown context is required")
	}
	manager.mu.Lock()
	ids := make([]string, 0, len(manager.active))
	profiles := make(map[string]string, len(manager.active))
	for id, entry := range manager.active {
		ids = append(ids, id)
		profiles[id] = entry.profile.ID
	}
	manager.mu.Unlock()
	slices.Sort(ids)
	var result error
	for _, id := range ids {
		result = errors.Join(result, manager.Stop(ctx, profiles[id], id))
	}
	return result
}

func (manager *Manager) run(ctx context.Context, entry *activeMirror) {
	defer close(entry.done)
	_ = entry.relay.run(ctx)
	entry.cancel()
	manager.mu.Lock()
	if manager.active[entry.task.ID] == entry {
		delete(manager.active, entry.task.ID)
	}
	manager.mu.Unlock()
}

func normalizeTargets(input []LocalTarget) ([]LocalTarget, []remote.MirrorPort, error) {
	if len(input) == 0 || len(input) > 64 {
		return nil, nil, errors.New("mirror requires one to 64 local targets")
	}
	targets := make([]LocalTarget, len(input))
	ports := make([]remote.MirrorPort, len(input))
	seen := make(map[string]struct{}, len(input))
	for index, target := range input {
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		if target.Protocol == "" {
			target.Protocol = mirrorProtocolTCP
		}
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = mirrorLoopbackHost
		}
		if target.LocalPort == 0 && target.ServicePort > 0 && target.ServicePort <= 65535 {
			target.LocalPort = uint16(target.ServicePort)
		}
		if target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort == 0 ||
			(target.Protocol != mirrorProtocolTCP && target.Protocol != mirrorProtocolUDP) || !validLocalHost(target.LocalHost) {
			return nil, nil, errors.New("mirror local target is invalid")
		}
		key := targetKey(target.Protocol, target.ServicePort)
		if _, exists := seen[key]; exists {
			return nil, nil, errors.New("mirror Service ports must be unique")
		}
		seen[key] = struct{}{}
		targets[index] = target
		ports[index] = remote.MirrorPort{ServicePort: target.ServicePort, Protocol: target.Protocol}
	}
	return targets, ports, nil
}

func validLocalHost(host string) bool {
	if address := net.ParseIP(host); address != nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	if len(host) > 253 || strings.ContainsAny(host, " /\\\t\r\n") {
		return false
	}
	for label := range strings.SplitSeq(host, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
				(character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func matchTaskTargets(task remote.MirrorTask, targets []LocalTarget) error {
	if len(task.Ports) != len(targets) {
		return errors.New("gateway changed the requested Mirror ports")
	}
	want := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		want[targetKey(target.Protocol, target.ServicePort)] = struct{}{}
	}
	for _, port := range task.Ports {
		key := targetKey(port.Protocol, port.ServicePort)
		if _, exists := want[key]; !exists {
			return errors.New("gateway changed the requested Mirror ports")
		}
		delete(want, key)
	}
	return nil
}

func targetKey(protocol string, port int32) string {
	return strings.ToLower(protocol) + "/" + strconv.Itoa(int(port))
}
