package app

import (
	"path/filepath"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientexchange "github.com/fengqi-dev/kube-loop/internal/client/exchange"
	clientmirror "github.com/fengqi-dev/kube-loop/internal/client/mirror"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh/sshserver"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientpreview "github.com/fengqi-dev/kube-loop/internal/client/preview"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	clientremotesession "github.com/fengqi-dev/kube-loop/internal/client/remotesession"
	"github.com/fengqi-dev/kube-loop/internal/mcp"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func configureRemoteTaskManagers(
	application *App,
	layout utils.Layout,
	remoteClient *clientremote.Client,
	remoteSessions *clientremotesession.Manager,
	dataPlanes *clientdataplane.Manager,
) {
	remoteSSH, sshErr := clientpodssh.New(remoteClient, remoteSessions, clientpodssh.Config{
		HostTCPRegistrar: dataPlanes,
		ServerOptions: []localpodssh.Option{
			localpodssh.WithHostKeyPath(filepath.Join(layout.SecretsDir(), "ssh_host_ed25519")),
		},
	})
	if sshErr != nil {
		application.appendLog("ERROR", "Pod SSH Manager unavailable: "+sshErr.Error())
	} else {
		application.remoteSSH = remoteSSH
	}
	remoteForwards, portForwardErr := clientportforward.New(remoteClient, dataPlanes)
	if portForwardErr != nil {
		application.appendLog("ERROR", "Port Forward Manager unavailable: "+portForwardErr.Error())
	} else {
		application.remoteForwards = remoteForwards
	}
	remoteExchanges, exchangeErr := clientexchange.NewManager(
		remoteClient,
		clientexchange.Config{TrafficStreams: dataPlanes},
	)
	if exchangeErr != nil {
		application.appendLog("ERROR", "Exchange Manager unavailable: "+exchangeErr.Error())
	} else {
		application.remoteExchanges = remoteExchanges
	}
	remoteMirrors, mirrorErr := clientmirror.NewManager(remoteClient, clientmirror.Config{TrafficStreams: dataPlanes})
	if mirrorErr != nil {
		application.appendLog("ERROR", "Mirror Manager unavailable: "+mirrorErr.Error())
	} else {
		application.remoteMirrors = remoteMirrors
	}
	remotePreviews, previewErr := clientpreview.NewManager(
		remoteClient,
		clientpreview.Config{TrafficStreams: dataPlanes},
	)
	if previewErr != nil {
		application.appendLog("ERROR", "Preview Manager unavailable: "+previewErr.Error())
	} else {
		application.remotePreviews = remotePreviews
	}
}

func configureMCP(application *App, layout utils.Layout, version string) {
	mcpSettingsPath := filepath.Join(layout.ConfigDir(), "mcp.json")
	mcpStore, err := mcp.NewSystemConfigStoreForVersion(mcpSettingsPath, version)
	if err != nil {
		application.appendLog("ERROR", "MCP settings store unavailable: "+err.Error())
		return
	}
	mcpDependencies := mcp.RemoteDependencies{
		Profiles: application.profiles, ControlPlane: application.remote,
		Sessions: application.remoteSessions, DataPlanes: application.dataPlanes,
		ExecClient: application.remote,
	}
	if application.remoteExecs != nil {
		mcpDependencies.ExecLifecycle = application.remoteExecs
	}
	if application.remoteFiles != nil {
		mcpDependencies.Files = application.remoteFiles
	}
	if application.remoteForwards != nil {
		mcpDependencies.Forwards = application.remoteForwards
	}
	if application.remoteExchanges != nil {
		mcpDependencies.Exchanges = application.remoteExchanges
	}
	if application.remoteMirrors != nil {
		mcpDependencies.Mirrors = application.remoteMirrors
	}
	if application.remotePreviews != nil {
		mcpDependencies.Previews = application.remotePreviews
	}
	mcpBackend, err := mcp.NewRemoteBackend(mcpDependencies)
	if err != nil {
		application.appendLog("ERROR", "MCP Gateway backend unavailable: "+err.Error())
		return
	}
	mcpController, err := mcp.NewController(mcpBackend, mcpStore, version, application.appendLog)
	if err != nil {
		application.appendLog("ERROR", "MCP controller unavailable: "+err.Error())
		return
	}
	application.mcp = mcpController
}
