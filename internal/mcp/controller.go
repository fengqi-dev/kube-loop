package mcp

import (
	"errors"
	"fmt"
	"log"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/filemanager"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"github.com/fengqi-dev/kube-loop/internal/store"
)

// Controller owns the embedded MCP server and persists settings via store.
type Controller struct {
	server *Server
	store  *store.Store
	logs   *session.Manager
}

// NewController wires a Backend over provider/manager/file transfers and loads store config.
func NewController(
	provider *cluster.Provider,
	manager *session.Manager,
	files *filemanager.Manager,
	stateStore *store.Store,
	version string,
) *Controller {
	c := &Controller{
		server: NewServer(managerBackend{
			provider: provider,
			manager:  manager,
			executor: provider,
			files:    files,
		}, version),
		store: stateStore,
		logs:  manager,
	}
	c.server.SetErrorHandler(func(err error) {
		c.appendLog("ERROR", fmt.Sprintf("MCP server stopped unexpectedly: %v", err))
	})
	if stateStore != nil {
		c.server.Configure(stateStore.MCP())
	}
	return c
}

// StartFromStore enables the listener when persisted config says Enabled.
func (c *Controller) StartFromStore() {
	if c == nil || c.server == nil || c.store == nil {
		return
	}
	cfg := c.store.MCP()
	if !cfg.Enabled {
		c.server.Configure(cfg)
		c.appendLog("INFO", "MCP server disabled by saved configuration")
		return
	}
	c.appendLog("INFO", fmt.Sprintf("starting MCP server on port %d", cfg.Port))
	var err error
	cfg, err = ensureToken(cfg)
	if err != nil {
		c.appendLog("ERROR", fmt.Sprintf("prepare MCP authentication: %v", err))
		return
	}
	if err := c.persist(cfg); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("persist MCP configuration: %v", err))
		return
	}
	if err := c.server.Apply(); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("start MCP server: %v", err))
		return
	}
	c.appendLog("INFO", "MCP server listening at "+c.server.Status().URL)
}

// Stop shuts down the HTTP listener.
func (c *Controller) Stop() error {
	if c == nil || c.server == nil {
		return nil
	}
	if !c.server.Status().Listening {
		return nil
	}
	c.appendLog("INFO", "stopping MCP server")
	if err := c.server.Stop(); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("stop MCP server: %v", err))
		return err
	}
	c.appendLog("INFO", "MCP server stopped")
	return nil
}

// Status returns runtime + config for the UI.
func (c *Controller) Status() Status {
	if c == nil || c.server == nil {
		return Status{Port: store.DefaultMCPPort}
	}
	return c.server.Status()
}

// SetEnabled turns the MCP server on or off and persists the choice.
func (c *Controller) SetEnabled(enabled bool) error {
	if c == nil || c.server == nil {
		return errors.New("mcp server unavailable")
	}
	cfg := c.server.Config()
	cfg.Enabled = enabled
	var err error
	cfg, err = ensureToken(cfg)
	if err != nil {
		c.appendLog("ERROR", fmt.Sprintf("prepare MCP authentication: %v", err))
		return err
	}
	if err := c.persist(cfg); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("persist MCP enabled=%t: %v", enabled, err))
		return err
	}
	if err := c.server.SetEnabled(enabled); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("set MCP enabled=%t: %v", enabled, err))
		return err
	}
	if enabled {
		c.appendLog("INFO", "MCP server enabled at "+c.server.Status().URL)
	} else {
		c.appendLog("INFO", "MCP server disabled")
	}
	return nil
}

// SetPort updates the listen port and persists it.
func (c *Controller) SetPort(port int) error {
	if c == nil || c.server == nil {
		return errors.New("mcp server unavailable")
	}
	if port <= 0 || port > 65535 {
		err := fmt.Errorf("invalid mcp port %d", port)
		c.appendLog("ERROR", err.Error())
		return err
	}
	cfg := c.server.Config()
	cfg.Port = port
	if err := c.persist(cfg); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("persist MCP port %d: %v", port, err))
		return err
	}
	if err := c.server.SetPort(port); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("set MCP port %d: %v", port, err))
		return err
	}
	c.appendLog("INFO", fmt.Sprintf("MCP server port set to %d", port))
	return nil
}

// SetTokenEnabled turns Bearer token auth on or off and persists the choice.
func (c *Controller) SetTokenEnabled(enabled bool) error {
	if c == nil || c.server == nil {
		return errors.New("mcp server unavailable")
	}
	cfg := c.server.Config()
	cfg.TokenEnabled = enabled
	var err error
	cfg, err = ensureToken(cfg)
	if err != nil {
		c.appendLog("ERROR", fmt.Sprintf("prepare MCP authentication: %v", err))
		return err
	}
	if err := c.persist(cfg); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("persist MCP token authentication: %v", err))
		return err
	}
	if err := c.server.SetTokenEnabled(enabled); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("set MCP token authentication enabled=%t: %v", enabled, err))
		return err
	}
	c.appendLog("INFO", fmt.Sprintf("MCP token authentication enabled=%t", enabled))
	return nil
}

// RegenerateToken replaces the bearer token when token auth is enabled.
func (c *Controller) RegenerateToken() (string, error) {
	if c == nil || c.server == nil {
		return "", errors.New("mcp server unavailable")
	}
	cfg := c.server.Config()
	if !cfg.TokenEnabled {
		err := errors.New("enable MCP token auth first")
		c.appendLog("WARN", "regenerate MCP token: "+err.Error())
		return "", err
	}
	token, err := GenerateToken()
	if err != nil {
		c.appendLog("ERROR", fmt.Sprintf("generate MCP authentication token: %v", err))
		return "", err
	}
	cfg.Token = token
	if err := c.persist(cfg); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("persist regenerated MCP token: %v", err))
		return "", err
	}
	if err := c.server.SetToken(token); err != nil {
		c.appendLog("ERROR", fmt.Sprintf("apply regenerated MCP token: %v", err))
		return "", err
	}
	c.appendLog("INFO", "MCP authentication token regenerated")
	return token, nil
}

// InstallClient writes the KubeLoop MCP endpoint into a client user config
// (claude, codex, cursor, or vscode). Enables the local MCP server if needed.
func (c *Controller) InstallClient(client string) (InstallResult, error) {
	if c == nil || c.server == nil {
		return InstallResult{}, errors.New("mcp server unavailable")
	}
	status := c.server.Status()
	if !status.Enabled || !status.Listening {
		if err := c.SetEnabled(true); err != nil {
			return InstallResult{}, err
		}
		status = c.server.Status()
	}
	if status.URL == "" {
		err := errors.New("mcp server is not ready")
		c.appendLog("ERROR", "install MCP client configuration: "+err.Error())
		return InstallResult{}, err
	}
	if status.TokenEnabled && status.Token == "" {
		err := errors.New("mcp token is not ready")
		c.appendLog("ERROR", "install MCP client configuration: "+err.Error())
		return InstallResult{}, err
	}
	token := ""
	if status.TokenEnabled {
		token = status.Token
	}
	c.appendLog("INFO", "installing MCP client configuration for "+client)
	result, err := InstallClientConfig(client, status.URL, token)
	if err != nil {
		c.appendLog("ERROR", fmt.Sprintf("install MCP client configuration for %s: %v", client, err))
		return InstallResult{}, err
	}
	c.appendLog("INFO", "MCP client configuration installed for "+client)
	return result, nil
}

func (c *Controller) persist(cfg store.MCPConfig) error {
	if c.store == nil {
		return errors.New("state store unavailable")
	}
	cfg = store.MCPConfig{
		Enabled:      cfg.Enabled,
		Port:         cfg.Port,
		TokenEnabled: cfg.TokenEnabled,
		Token:        cfg.Token,
	}
	if cfg.Port <= 0 {
		cfg.Port = store.DefaultMCPPort
	}
	if err := c.store.SetMCP(cfg); err != nil {
		return err
	}
	c.server.Configure(cfg)
	return nil
}

func ensureToken(cfg store.MCPConfig) (store.MCPConfig, error) {
	if !cfg.TokenEnabled || cfg.Token != "" {
		return cfg, nil
	}
	token, err := GenerateToken()
	if err != nil {
		return cfg, err
	}
	cfg.Token = token
	if cfg.Port <= 0 {
		cfg.Port = store.DefaultMCPPort
	}
	return cfg, nil
}

func (c *Controller) appendLog(level, message string) {
	if c != nil && c.logs != nil {
		c.logs.AppendLog(level, message)
		return
	}
	log.Printf("%s: %s", level, message)
}
