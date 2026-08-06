package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"time"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ContextInfo is one kubeconfig context entry for the UI.
type ContextInfo struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	Server    string `json:"server,omitempty"`
	User      string `json:"user,omitempty"`
	Namespace string `json:"namespace,omitempty"`
	Source    string `json:"source,omitempty"`
	Current   bool   `json:"current"`
}

// KubeconfigFileInfo describes one kubeconfig source path shown in the UI.
type KubeconfigFileInfo struct {
	Path    string `json:"path"`
	Default bool   `json:"default"`
}

// ClusterInventory is the reload payload for the Clusters page.
type ClusterInventory struct {
	Contexts []ContextInfo        `json:"contexts"`
	Files    []KubeconfigFileInfo `json:"files"`
}

// ProbeResult is the reachability check for one context.
type ProbeResult struct {
	Context   string `json:"context"`
	OK        bool   `json:"ok"`
	Version   string `json:"version,omitempty"`
	LatencyMs int64  `json:"latencyMs,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Provider loads kubeconfig contexts and talks to the Kubernetes API.
type Provider struct {
	mu         sync.RWMutex
	extraFiles []string
	userAgent  string
}

func NewProvider() *Provider {
	return &Provider{userAgent: "kube-loop/dev"}
}

// SetUserAgent sets the Kubernetes client User-Agent from the app version
// (e.g. "dev" or "v0.2.0" → "kube-loop/dev" / "kube-loop/v0.2.0").
func (p *Provider) SetUserAgent(version string) {
	if version == "" {
		version = "dev"
	}
	p.mu.Lock()
	p.userAgent = "kube-loop/" + version
	p.mu.Unlock()
}

// SetExtraKubeconfigFiles replaces the user-added kubeconfig path list.
func (p *Provider) SetExtraKubeconfigFiles(paths []string) {
	normalized := normalizeKubeconfigPaths(paths)
	p.mu.Lock()
	p.extraFiles = normalized
	p.mu.Unlock()
}

// ExtraKubeconfigFiles returns a copy of user-added kubeconfig paths.
func (p *Provider) ExtraKubeconfigFiles() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.extraFiles)
}

func (p *Provider) loadingRules() *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	p.mu.RLock()
	extras := slices.Clone(p.extraFiles)
	p.mu.RUnlock()
	if len(extras) == 0 {
		return rules
	}
	precedence := slices.Clone(rules.GetLoadingPrecedence())
	precedence = append(precedence, extras...)
	rules.Precedence = precedence
	rules.ExplicitPath = ""
	return rules
}

// DefaultKubeconfigPaths returns the system/default kubeconfig loading paths.
func (p *Provider) DefaultKubeconfigPaths() []string {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	return slices.Clone(rules.GetLoadingPrecedence())
}

// KubeconfigFiles lists default + user-added sources for the UI.
func (p *Provider) KubeconfigFiles() []KubeconfigFileInfo {
	defaults := p.DefaultKubeconfigPaths()
	extras := p.ExtraKubeconfigFiles()
	files := make([]KubeconfigFileInfo, 0, len(defaults)+len(extras))
	seen := make(map[string]struct{}, len(defaults)+len(extras))
	for _, path := range defaults {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, KubeconfigFileInfo{Path: path, Default: true})
	}
	for _, path := range extras {
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		files = append(files, KubeconfigFileInfo{Path: path, Default: false})
	}
	return files
}

// Inventory reloads contexts and file sources.
func (p *Provider) Inventory() (ClusterInventory, error) {
	contexts, err := p.Contexts()
	if err != nil {
		return ClusterInventory{}, err
	}
	return ClusterInventory{
		Contexts: contexts,
		Files:    p.KubeconfigFiles(),
	}, nil
}

// ValidateKubeconfigFile ensures path is a readable kubeconfig with at least one context.
func ValidateKubeconfigFile(path string) error {
	path = filepath.Clean(path)
	if path == "" || path == "." {
		return fmt.Errorf("kubeconfig path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat kubeconfig: %w", err)
	}
	if info.IsDir() {
		return fmt.Errorf("kubeconfig path is a directory: %s", path)
	}
	cfg, err := clientcmd.LoadFromFile(path)
	if err != nil {
		return fmt.Errorf("load kubeconfig: %w", err)
	}
	if len(cfg.Contexts) == 0 {
		return fmt.Errorf("kubeconfig has no contexts: %s", path)
	}
	return nil
}

func (p *Provider) Contexts() ([]ContextInfo, error) {
	paths := p.loadingPaths()
	if len(paths) == 0 {
		return nil, fmt.Errorf("no kubeconfig files found")
	}

	byName := make(map[string]ContextInfo)
	var currentContext string
	loaded := 0
	var lastErr error
	for _, path := range paths {
		cfg, err := clientcmd.LoadFromFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			lastErr = err
			continue
		}
		loaded++
		if cfg.CurrentContext != "" {
			currentContext = cfg.CurrentContext
		}
		for name, value := range cfg.Contexts {
			if value == nil {
				continue
			}
			byName[name] = contextInfoFrom(name, value, cfg, path)
		}
	}
	if loaded == 0 {
		if lastErr != nil {
			return nil, fmt.Errorf("load kubeconfig: %w", lastErr)
		}
		return nil, fmt.Errorf("load kubeconfig: no readable files")
	}

	items := make([]ContextInfo, 0, len(byName))
	for name, item := range byName {
		item.Current = name == currentContext
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Current != items[j].Current {
			return items[i].Current
		}
		return items[i].Name < items[j].Name
	})
	return items, nil
}

func (p *Provider) loadingPaths() []string {
	rules := p.loadingRules()
	return rules.GetLoadingPrecedence()
}

func contextInfoFrom(
	name string, value *clientcmdapi.Context, cfg *clientcmdapi.Config, source string,
) ContextInfo {
	info := ContextInfo{
		Name:      name,
		Cluster:   value.Cluster,
		User:      value.AuthInfo,
		Namespace: value.Namespace,
		Source:    source,
	}
	if cluster := cfg.Clusters[value.Cluster]; cluster != nil {
		info.Server = cluster.Server
	}
	return info
}

func normalizeKubeconfigPaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.Clean(path)
		if path == "" || path == "." {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}

// ServerVersion returns the Kubernetes API server GitVersion for contextName.
func (p *Provider) ServerVersion(ctx context.Context, contextName string) (string, error) {
	if contextName == "" {
		return "", fmt.Errorf("context is required")
	}
	client, err := p.client(contextName)
	if err != nil {
		return "", err
	}
	version, err := client.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	if version == nil || version.GitVersion == "" {
		return "", fmt.Errorf("empty server version")
	}
	return version.GitVersion, nil
}

// Probe checks whether the Kubernetes API for contextName is reachable.
func (p *Provider) Probe(ctx context.Context, contextName string) ProbeResult {
	result := ProbeResult{Context: contextName}
	if contextName == "" {
		result.Error = "context is required"
		return result
	}
	start := time.Now()
	version, err := p.ServerVersion(ctx, contextName)
	result.LatencyMs = time.Since(start).Milliseconds()
	if err != nil {
		result.Error = err.Error()
		return result
	}
	result.OK = true
	result.Version = version
	return result
}
