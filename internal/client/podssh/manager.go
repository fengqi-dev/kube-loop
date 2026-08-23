package podssh

import (
	"errors"
	"sync"

	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/socksbridge"
)

type SessionSource interface {
	Current(string) (remote.Session, error)
}

type Config struct {
	ServerOptions    []localpodssh.Option
	HostTCPRegistrar HostTCPRegistrar
}

type HostTCPRegistrar interface {
	SetHostTCPHandler(string, socksbridge.HostTCPHandler) error
}

var ErrClosed = errors.New("pod SSH manager is closed")

type Request struct {
	ProfileID  string   `json:"profileId"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container,omitempty"`
	PodIP      string   `json:"podIp"`
	Ready      bool     `json:"ready"`
	Containers []string `json:"containers"`
}

type Info struct {
	ID         string   `json:"id"`
	ProfileID  string   `json:"profileId"`
	SessionID  string   `json:"sessionId"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container"`
	Containers []string `json:"containers"`
	PodIP      string   `json:"podIp"`
	Address    string   `json:"address"`
	Port       uint16   `json:"port"`
	Command    string   `json:"command"`
	State      string   `json:"state"`
}

type activeEndpoint struct {
	profile profile.Profile
	session remote.Session
	target  localpodssh.Target
	info    Info
}

type Manager struct {
	client   clientexec.Client
	sessions SessionSource
	server   *localpodssh.Server
	hostTCP  HostTCPRegistrar

	lifecycle sync.RWMutex
	closed    bool
	mu        sync.Mutex
	active    map[string]*activeEndpoint
	starting  map[string]struct{}
}

func New(client clientexec.Client, sessions SessionSource, config Config) (*Manager, error) {
	if client == nil || sessions == nil {
		return nil, errors.New("pod SSH remote exec client and Session source are required")
	}
	manager := &Manager{
		client: client, sessions: sessions, hostTCP: config.HostTCPRegistrar,
		active: make(map[string]*activeEndpoint), starting: make(map[string]struct{}),
	}
	manager.server = localpodssh.NewServer(remoteExecutor{manager: manager}, config.ServerOptions...)
	return manager, nil
}
