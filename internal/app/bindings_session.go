package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	helperinstall "github.com/fengqi-dev/kube-loop/internal/helper/install"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
	"k8s.io/apimachinery/pkg/util/validation"
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

// SetShareGateway selects a cluster-wide shared Gateway or a stable Gateway
// dedicated to this desktop client. The change takes effect on the next connect.
func (a *App) SetShareGateway(shared bool) error {
	if a.store == nil {
		return errors.New("state store is unavailable")
	}
	state := a.manager.State()
	if state.Phase != session.PhaseIdle && state.Phase != session.PhaseError {
		return errors.New("disconnect before changing the Gateway sharing setting")
	}
	if err := a.store.SetShareGateway(shared); err != nil {
		return err
	}
	namespace, name := gatewayResource(
		shared, a.store.GatewayID(), a.store.GatewayNamespace(),
	)
	a.provider.SetGatewayResource(namespace, name)
	a.manager.SetGatewayResource(namespace, name)
	return nil
}

// SetGatewayNamespace updates the exact namespace used for Gateway resources.
func (a *App) SetGatewayNamespace(namespace string) error {
	if a.store == nil {
		return errors.New("state store is unavailable")
	}
	state := a.manager.State()
	if state.Phase != session.PhaseIdle && state.Phase != session.PhaseError {
		return errors.New("disconnect before changing the Gateway namespace")
	}
	namespace = strings.TrimSpace(namespace)
	if problems := validation.IsDNS1123Label(namespace); len(problems) > 0 {
		return fmt.Errorf("invalid Gateway namespace: %s", strings.Join(problems, "; "))
	}
	if err := a.store.SetGatewayNamespace(namespace); err != nil {
		return err
	}
	resolvedNS, name := gatewayResource(
		a.store.ShareGateway(), a.store.GatewayID(), namespace,
	)
	a.provider.SetGatewayResource(resolvedNS, name)
	a.manager.SetGatewayResource(resolvedNS, name)
	return nil
}

func (a *App) GetGatewayTransport(contextName string) store.GatewayTransport {
	return a.manager.GatewayTransport(contextName)
}

func (a *App) SetGatewayTransport(contextName string, config store.GatewayTransport) error {
	return a.manager.SetGatewayTransport(contextName, config)
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
