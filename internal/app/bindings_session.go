package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

func (a *App) Connect(contextName, namespace string) error {
	_ = a.manager.RememberSelection(contextName, namespace)
	return a.manager.Connect(a.ctx, session.Request{
		Context: contextName, Namespace: namespace,
	})
}

func (a *App) ConnectMode(contextName, namespace, mode string) error {
	_ = a.manager.RememberSelection(contextName, namespace)
	return a.manager.Connect(a.ctx, session.Request{
		Context: contextName, Namespace: namespace, Mode: session.ConnectionMode(mode),
	})
}

func (a *App) Disconnect() error {
	return a.manager.Disconnect()
}

func (a *App) GetManualNetwork(contextName string) cluster.ManualNetwork {
	return a.manager.ManualNetwork(contextName)
}

func (a *App) SetManualNetwork(contextName string, network cluster.ManualNetwork) error {
	return a.manager.SetManualNetwork(contextName, network)
}

func (a *App) SetDNSNamespace(contextName, namespace string) error {
	return a.manager.SetDNSNamespace(contextName, namespace)
}

func (a *App) GetHostAliases(contextName string) []store.HostAliasSpec {
	return a.manager.HostAliases(contextName)
}

func (a *App) SetHostAliases(contextName string, items []store.HostAliasSpec) error {
	return a.manager.SetHostAliases(contextName, items)
}

func (a *App) GatewayInstallManifest() string {
	return a.manager.GatewayInstallManifest()
}

// GetSingBoxConfig returns the active session's generated sing-box config JSON.
func (a *App) GetSingBoxConfig() (string, error) {
	raw, err := a.manager.SingBoxConfig()
	if err != nil {
		a.manager.AppendLog("WARN", fmt.Sprintf("read active sing-box config: %v", err))
		return "", err
	}
	a.manager.AppendLog("INFO", "active sing-box config retrieved")
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		return string(raw), nil
	}
	return pretty.String(), nil
}

func (a *App) HelperStatus() helper.Status {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return helper.GetStatus(ctx)
}

func (a *App) InstallHelper() error {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.manager.AppendLog("INFO", "installing privileged helper")
	if err := helperinstall.EnsureInstall(ctx); err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("install privileged helper: %v", err))
		return err
	}
	status := helper.GetStatus(ctx)
	a.manager.AppendLog("INFO", fmt.Sprintf(
		"privileged helper ready: version=%s protocol=%d coreReady=%t",
		status.Version, status.Protocol, status.CoreReady,
	))
	return nil
}

func (a *App) UninstallHelper() error {
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	a.manager.AppendLog("INFO", "uninstalling privileged helper")
	if err := helperinstall.Uninstall(ctx); err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("uninstall privileged helper: %v", err))
		return err
	}
	a.manager.AppendLog("INFO", "privileged helper uninstalled")
	return nil
}
