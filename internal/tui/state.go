package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/fengqi-dev/kube-loop/internal/authconfig"
	clientauth "github.com/fengqi-dev/kube-loop/internal/client/auth"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientdiscovery "github.com/fengqi-dev/kube-loop/internal/client/discovery"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientexec "github.com/fengqi-dev/kube-loop/internal/client/exec"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
	singboxruntime "github.com/fengqi-dev/kube-loop/internal/singbox/runtime"
	"github.com/fengqi-dev/kube-loop/internal/userpaths"
)

// State is the composition root for the core TUI client.
type State struct {
	version     string
	profiles    *clientprofile.Store
	credentials credentials.Store
	discovery   *clientdiscovery.Client
	auth        *clientauth.Client
	remote      *clientremote.Client
	sessions    *clientremotesession.Manager
	dataPlanes  *clientdataplane.Manager
	forwards    *clientportforward.Manager
	exchanges   *clientexchange.Manager
	mirrors     *clientmirror.Manager
	previews    *clientpreview.Manager
	podSSH      *clientpodssh.Manager
	execs       *clientexec.Manager
	execEvents  chan clientexec.Event
	configPath  string

	ctx    context.Context
	cancel context.CancelFunc
}

type AuthSession struct {
	Authenticated    bool
	UserName         string
	AccessExpiresAt  string
	RefreshExpiresAt string
}

func NewState(version string) (*State, error) {
	layout, err := userpaths.ForVersion(version)
	if err != nil {
		return nil, fmt.Errorf("resolve user layout: %w", err)
	}
	if err := layout.Ensure(); err != nil {
		return nil, fmt.Errorf("initialize user layout: %w", err)
	}
	profileStore, err := clientprofile.Open(filepath.Join(layout.ConfigDir(), "servers.json"))
	if err != nil {
		return nil, fmt.Errorf("open profile store: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	credentialStore := credentials.NewSystemStoreForVersion(version)
	discoveryClient := clientdiscovery.New(clientdiscovery.Config{ClientVersion: version})
	authClient := clientauth.New(clientauth.Config{
		OpenBrowser: func(target string) error { return openBrowser(ctx, target) }, BrowserCallback: func() {},
		ClientID: authconfig.TUIClientID, RedirectURI: authconfig.TUIRedirectURI,
		LoopbackCallback: true,
	})
	remoteClient, err := clientremote.New(credentialStore, authClient, clientremote.Config{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create remote client: %w", err)
	}
	remoteSessions, err := clientremotesession.New(remoteClient, clientremotesession.Config{})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create session manager: %w", err)
	}
	dataPlanes, err := clientdataplane.NewManager(remoteSessions, clientdataplane.Config{
		ClientVersion: version,
		TUNStarter:    singboxruntime.NewPrivileged(),
		OnStatus:      func(clientdataplane.StatusEvent) {},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create data plane manager: %w", err)
	}
	podSSH, err := clientpodssh.New(remoteClient, remoteSessions, clientpodssh.Config{
		HostTCPRegistrar: dataPlanes,
		ServerOptions: []localpodssh.Option{
			localpodssh.WithHostKeyPath(filepath.Join(layout.SecretsDir(), "ssh_host_ed25519")),
		},
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create pod SSH manager: %w", err)
	}
	forwards, err := clientportforward.New(remoteClient, dataPlanes)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create port forward manager: %w", err)
	}
	exchanges, err := clientexchange.NewManager(remoteClient, clientexchange.Config{TrafficStreams: dataPlanes})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create exchange manager: %w", err)
	}
	mirrors, err := clientmirror.NewManager(remoteClient, clientmirror.Config{TrafficStreams: dataPlanes})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create mirror manager: %w", err)
	}
	previews, err := clientpreview.NewManager(remoteClient, clientpreview.Config{TrafficStreams: dataPlanes})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create preview manager: %w", err)
	}
	execEvents := make(chan clientexec.Event, 256)
	execs, err := clientexec.NewManager(remoteClient, clientexec.ManagerConfig{OnEvent: func(event clientexec.Event) {
		select {
		case execEvents <- event:
		default:
		}
	}})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create exec manager: %w", err)
	}
	return &State{
		version: version, profiles: profileStore, credentials: credentialStore,
		discovery: discoveryClient, auth: authClient, remote: remoteClient,
		sessions: remoteSessions, dataPlanes: dataPlanes, forwards: forwards,
		exchanges: exchanges, mirrors: mirrors, previews: previews,
		podSSH: podSSH, execs: execs, execEvents: execEvents,
		configPath: filepath.Join(layout.ConfigDir(), "tui.yaml"), ctx: ctx, cancel: cancel,
	}, nil
}

func (s *State) Close() {
	s.cancel()
	_ = s.execs.Shutdown()
	_ = s.podSSH.Shutdown()
	_ = s.forwards.Shutdown(s.ctx)
	_ = s.exchanges.Shutdown(s.ctx)
	_ = s.mirrors.Shutdown(s.ctx)
	_ = s.previews.Shutdown(s.ctx)
	_ = s.dataPlanes.Shutdown()
	_ = s.sessions.Shutdown(s.ctx)
}

func (s *State) Snapshot() clientprofile.State { return s.profiles.Snapshot() }

func (s *State) ActiveProfile() (clientprofile.Profile, bool) {
	state := s.profiles.Snapshot()
	for _, profile := range state.Profiles {
		if profile.ID == state.ActiveProfileID {
			return profile, true
		}
	}
	return clientprofile.Profile{}, false
}

func (s *State) AuthStatus(profileID string) (AuthSession, error) {
	credential, err := s.credentials.Get(profileID)
	if errors.Is(err, credentials.ErrNotFound) {
		return AuthSession{}, nil
	}
	if err != nil {
		return AuthSession{}, err
	}
	return AuthSession{
		Authenticated:    credential.AccessToken != "" && credential.RefreshToken != "",
		UserName:         credential.UserName,
		AccessExpiresAt:  credential.AccessExpiresAt.Format("2006-01-02 15:04 MST"),
		RefreshExpiresAt: credential.RefreshExpiresAt.Format("2006-01-02 15:04 MST"),
	}, nil
}

func openBrowser(ctx context.Context, url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.CommandContext(ctx, "open", url).Start()
	case "linux":
		return exec.CommandContext(ctx, "xdg-open", url).Start()
	case "windows":
		return exec.CommandContext(ctx, "rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}
