package dataplane

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func (runtime *Runtime) Status() Status {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	return runtime.status
}

func (runtime *Runtime) StartTUN(ctx context.Context) (Status, error) {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	return runtime.startTUNLocked(ctx)
}

func (runtime *Runtime) startTUNLocked(ctx context.Context) (Status, error) {
	if runtime.ctx != nil && runtime.ctx.Err() != nil {
		return Status{}, errors.New("data Plane runtime is closed")
	}
	if runtime.tun != nil {
		return runtime.status, nil
	}
	if runtime.tunStarter == nil {
		return Status{}, errors.New("tUN runtime is unavailable")
	}
	select {
	case <-runtime.done:
		return Status{}, errors.New("data Plane runtime is closed")
	default:
	}
	spec := runtime.session.NetworkSpec
	// The caller context bounds startup only. Once ready, the TUN belongs to
	// the Data Plane Runtime and must survive the Wails RPC context ending.
	// The sing-box runtime watches its context for the entire process lifetime.
	tunCtx, tunCancel := context.WithCancel(runtime.ctx)
	stopStartupCancel := context.AfterFunc(ctx, tunCancel)
	namespace := runtime.dnsNamespace
	if namespace == "" {
		namespace = runtime.session.Namespace
	}
	runtime.appendSOCKSLog("starting TUN")
	core, err := runtime.tunStarter.Start(tunCtx, singbox.NetworkSpec{
		PodCIDRs: append([]string(nil), spec.PodCIDRs...), PodIPs: append([]string(nil), spec.PodIPs...),
		ServiceCIDRs: append([]string(nil), spec.ServiceCIDRs...),
		ServiceIPs:   append([]string(nil), spec.ServiceIPs...), DNSServer: spec.DNSServer,
		ClusterDomains: append([]string(nil), spec.ClusterDomains...),
	}, runtime.status.SOCKSAddress, namespace, append([]singbox.HostAlias{}, runtime.hostAliases...))
	stopStartupCancel()
	if err != nil {
		runtime.appendSOCKSLog("TUN start failed: " + err.Error())
		tunCancel()
		return Status{}, fmt.Errorf("start TUN: %w", err)
	}
	if err := ctx.Err(); err != nil {
		tunCancel()
		_ = core.Close()
		return Status{}, fmt.Errorf("start TUN: %w", err)
	}
	runtime.appendSOCKSLog("TUN core ready")
	runtime.tun = core
	runtime.tunCancel = tunCancel
	runtime.status.Mode = ModeTUN
	runtime.tunWG.Go(func() { runtime.watchTUN(core) })
	return runtime.status, nil
}

func (runtime *Runtime) StopTUN() (Status, error) {
	runtime.stateMu.Lock()
	core := runtime.tun
	cancel := runtime.tunCancel
	runtime.tun = nil
	runtime.tunCancel = nil
	runtime.status.Mode = ModeSOCKS
	status := runtime.status
	runtime.stateMu.Unlock()
	if core == nil {
		return status, nil
	}
	if cancel != nil {
		cancel()
	}
	if err := core.Close(); err != nil {
		return Status{}, err
	}
	return status, nil
}

func (runtime *Runtime) Logs(ctx context.Context) ([]string, error) {
	runtime.stateMu.Lock()
	core := runtime.tun
	runtime.stateMu.Unlock()
	runtime.logMu.Lock()
	logs := slices.Clone(runtime.socksLogs)
	runtime.logMu.Unlock()
	if core == nil {
		return logs, nil
	}
	tunLogs, err := core.ReadLogs(ctx)
	if err != nil {
		return nil, err
	}
	for _, line := range tunLogs {
		logs = append(logs, "[TUN] "+line)
	}
	return logs, nil
}

func (runtime *Runtime) appendSOCKSLog(message string) {
	line := time.Now().Format("15:04:05") + " [SOCKS] " + message
	runtime.logMu.Lock()
	runtime.socksLogs = append(runtime.socksLogs, line)
	if len(runtime.socksLogs) > maxRuntimeLogLines {
		runtime.socksLogs = slices.Clone(runtime.socksLogs[len(runtime.socksLogs)-maxRuntimeLogLines:])
	}
	runtime.logMu.Unlock()
}

func (runtime *Runtime) ConfigJSON() ([]byte, error) {
	runtime.stateMu.Lock()
	defer runtime.stateMu.Unlock()
	if runtime.tun == nil {
		return nil, errors.New("tUN runtime is not running")
	}
	return append([]byte{}, runtime.tun.Config()...), nil
}

func (runtime *Runtime) UpdateDNSNamespace(ctx context.Context, namespace string) error {
	namespace = strings.TrimSpace(namespace)
	runtime.stateMu.Lock()
	core := runtime.tun
	effectiveNamespace := namespace
	if effectiveNamespace == "" {
		effectiveNamespace = runtime.session.Namespace
	}
	if core == nil {
		runtime.dnsNamespace = namespace
		runtime.stateMu.Unlock()
		return nil
	}
	runtime.stateMu.Unlock()
	if err := core.UpdateDNSNamespace(ctx, effectiveNamespace); err != nil {
		return err
	}
	runtime.stateMu.Lock()
	runtime.dnsNamespace = namespace
	runtime.stateMu.Unlock()
	return nil
}

func (runtime *Runtime) UpdateHostAliases(ctx context.Context, aliases []singbox.HostAlias) error {
	normalized, err := singbox.NormalizeHostAliases(aliases)
	if err != nil {
		return err
	}
	runtime.stateMu.Lock()
	core := runtime.tun
	if core == nil {
		runtime.hostAliases = normalized
		runtime.stateMu.Unlock()
		return nil
	}
	runtime.stateMu.Unlock()
	if err := core.UpdateHostAliases(ctx, normalized); err != nil {
		return err
	}
	runtime.stateMu.Lock()
	runtime.hostAliases = normalized
	runtime.stateMu.Unlock()
	return nil
}

func profileHostAliases(items []profile.HostAlias) []singbox.HostAlias {
	aliases := make([]singbox.HostAlias, len(items))
	for index, item := range items {
		aliases[index] = singbox.HostAlias{Domain: item.Domain, IP: item.IP}
	}
	return aliases
}
