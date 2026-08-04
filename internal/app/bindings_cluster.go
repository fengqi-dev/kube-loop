package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/locale"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

func (a *App) Bootstrap() (BootstrapData, error) {
	contexts, err := a.manager.Contexts()
	if err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("load kubeconfig contexts during bootstrap: %v", err))
		return BootstrapData{}, err
	}
	preferredContext, preferredNamespace := a.manager.PreferredSelection()
	selected := preferredContext
	if selected == "" || !contextExists(contexts, selected) {
		selected = ""
		for _, item := range contexts {
			if item.Current {
				selected = item.Name
				break
			}
		}
		if selected == "" && len(contexts) > 0 {
			selected = contexts[0].Name
		}
	}
	namespaces := []string{"default"}
	if selected != "" {
		if found, listErr := a.manager.Namespaces(a.ctx, selected); listErr == nil && len(found) > 0 {
			namespaces = found
		} else if listErr != nil {
			a.manager.AppendLog("WARN", fmt.Sprintf(
				"load namespaces during bootstrap for %s: %v", selected, listErr,
			))
		}
	}
	if preferredNamespace == "" || !slices.Contains(namespaces, preferredNamespace) {
		if slices.Contains(namespaces, "default") {
			preferredNamespace = "default"
		} else if len(namespaces) > 0 {
			preferredNamespace = namespaces[0]
		}
	}
	if preferredContext == "" || !contextExists(contexts, preferredContext) {
		preferredContext = selected
	}
	preferredMode := a.manager.PreferredConnectionMode(preferredContext)
	a.updateMu.RLock()
	updateState := a.updateState
	a.updateMu.RUnlock()
	return BootstrapData{
		Contexts: contexts, Namespaces: namespaces, Session: a.manager.State(),
		Update: updateState, PreferredContext: preferredContext,
		PreferredNamespace: preferredNamespace, PreferredMode: preferredMode,
		Platform:        goruntime.GOOS,
		KubeconfigFiles: a.provider.KubeconfigFiles(),
	}, nil
}

func (a *App) ReloadContexts() (cluster.ClusterInventory, error) {
	a.manager.AppendLog("INFO", "reloading kubeconfig contexts")
	inventory, err := a.provider.Inventory()
	if err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("reload kubeconfig contexts: %v", err))
		return cluster.ClusterInventory{}, err
	}
	a.manager.AppendLog("INFO", fmt.Sprintf(
		"kubeconfig contexts reloaded: contexts=%d files=%d",
		len(inventory.Contexts), len(inventory.Files),
	))
	return inventory, nil
}

func (a *App) AddKubeconfig() (cluster.ClusterInventory, error) {
	if a.ctx == nil {
		return cluster.ClusterInventory{}, errors.New("application is not ready")
	}
	s := locale.T()
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		Title: s.SelectKubeconfig,
		Filters: []runtime.FileFilter{{
			DisplayName: s.KubeconfigFilter, Pattern: "*.yaml;*.yml;*.conf;*",
		}},
	})
	if err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("open kubeconfig file picker: %v", err))
		return cluster.ClusterInventory{}, err
	}
	if path == "" {
		return a.provider.Inventory()
	}
	return a.AddKubeconfigPath(path)
}

func (a *App) AddKubeconfigPath(path string) (cluster.ClusterInventory, error) {
	if path == "" {
		return cluster.ClusterInventory{}, errors.New("kubeconfig path is required")
	}
	displayName := filepath.Base(path)
	a.manager.AppendLog("INFO", "adding kubeconfig file "+displayName)
	if err := cluster.ValidateKubeconfigFile(path); err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("validate kubeconfig %s: %v", displayName, err))
		return cluster.ClusterInventory{}, err
	}
	if a.store != nil {
		if err := a.store.AddKubeconfigFile(path); err != nil {
			a.manager.AppendLog("ERROR", fmt.Sprintf("save kubeconfig %s: %v", displayName, err))
			return cluster.ClusterInventory{}, err
		}
		a.provider.SetExtraKubeconfigFiles(a.store.KubeconfigFiles())
	} else {
		a.provider.SetExtraKubeconfigFiles(append(a.provider.ExtraKubeconfigFiles(), path))
	}
	inventory, err := a.provider.Inventory()
	if err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("reload contexts after adding %s: %v", displayName, err))
		return cluster.ClusterInventory{}, err
	}
	a.manager.AppendLog("INFO", fmt.Sprintf(
		"kubeconfig file added: %s contexts=%d", displayName, len(inventory.Contexts),
	))
	return inventory, nil
}

func (a *App) RemoveKubeconfig(path string) (cluster.ClusterInventory, error) {
	if path == "" {
		return cluster.ClusterInventory{}, errors.New("kubeconfig path is required")
	}
	displayName := filepath.Base(path)
	a.manager.AppendLog("INFO", "removing kubeconfig file "+displayName)
	state := a.manager.State()
	if sessionActive(state.Phase) {
		contexts, err := a.provider.Contexts()
		if err != nil {
			a.manager.AppendLog("ERROR", fmt.Sprintf(
				"inspect active kubeconfig before removing %s: %v", displayName, err,
			))
			return cluster.ClusterInventory{}, err
		}
		for _, item := range contexts {
			if item.Name == state.Context && item.Source == path {
				err := errors.New(
					"disconnect before removing the active kubeconfig",
				)
				a.manager.AppendLog("WARN", fmt.Sprintf("remove kubeconfig %s: %v", displayName, err))
				return cluster.ClusterInventory{}, err
			}
		}
	}
	if a.store != nil {
		if err := a.store.RemoveKubeconfigFile(path); err != nil {
			a.manager.AppendLog("ERROR", fmt.Sprintf("remove kubeconfig %s: %v", displayName, err))
			return cluster.ClusterInventory{}, err
		}
		a.provider.SetExtraKubeconfigFiles(a.store.KubeconfigFiles())
	} else {
		remaining := make([]string, 0)
		for _, existing := range a.provider.ExtraKubeconfigFiles() {
			if existing != path {
				remaining = append(remaining, existing)
			}
		}
		a.provider.SetExtraKubeconfigFiles(remaining)
	}
	inventory, err := a.provider.Inventory()
	if err != nil {
		a.manager.AppendLog("ERROR", fmt.Sprintf("reload contexts after removing %s: %v", displayName, err))
		return cluster.ClusterInventory{}, err
	}
	a.manager.AppendLog("INFO", fmt.Sprintf(
		"kubeconfig file removed: %s contexts=%d", displayName, len(inventory.Contexts),
	))
	return inventory, nil
}

func sessionActive(phase session.Phase) bool {
	switch phase {
	case session.PhaseConnected, session.PhaseChecking, session.PhaseInstalling,
		session.PhaseDiscovering, session.PhaseStarting:
		return true
	default:
		return false
	}
}

func (a *App) ProbeContext(contextName string) (cluster.ProbeResult, error) {
	if contextName == "" {
		return cluster.ProbeResult{}, errors.New("context is required")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	result := a.provider.Probe(probeCtx, contextName)
	if result.OK {
		a.manager.AppendLog("INFO", fmt.Sprintf(
			"cluster probe succeeded: context=%s version=%s latencyMs=%d",
			contextName, result.Version, result.LatencyMs,
		))
	} else {
		a.manager.AppendLog("WARN", fmt.Sprintf(
			"cluster probe failed: context=%s latencyMs=%d error=%s",
			contextName, result.LatencyMs, result.Error,
		))
	}
	state := a.manager.State()
	if result.OK && result.Version != "" &&
		state.Context == contextName && sessionActive(state.Phase) {
		a.manager.SetKubernetesVersion(result.Version)
	}
	return result, nil
}

func (a *App) RememberSelection(contextName, namespace string) error {
	return a.manager.RememberSelection(contextName, namespace)
}

func (a *App) Namespaces(contextName string) ([]string, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	return a.manager.Namespaces(a.ctx, contextName)
}

func (a *App) ListServices(contextName, namespace string) ([]cluster.ServiceInfo, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.manager.ListServices(ctx, contextName, namespace)
}

func (a *App) ListPods(contextName, namespace string) ([]cluster.PodInfo, error) {
	if contextName == "" {
		return nil, errors.New("context is required")
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return a.manager.ListPods(ctx, contextName, namespace)
}

func contextExists(contexts []cluster.ContextInfo, name string) bool {
	for _, item := range contexts {
		if item.Name == name {
			return true
		}
	}
	return false
}
