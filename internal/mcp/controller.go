package mcp

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
)

// Controller owns the embedded MCP listener and its settings store.
type Controller struct {
	server *Server
	store  ConfigStore
	log    *slog.Logger
}

func NewController(backend Backend, configStore ConfigStore, version string, logger *slog.Logger) (*Controller, error) {
	if backend == nil {
		return nil, errors.New("MCP Control Plane backend is required")
	}
	if configStore == nil {
		return nil, errors.New("MCP settings store is required")
	}
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	config, err := configStore.Load()
	if err != nil {
		return nil, err
	}
	controller := &Controller{
		server: NewServer(backend, version), store: configStore, log: logger,
	}
	controller.server.Configure(config)
	controller.server.SetErrorHandler(func(err error) {
		controller.logError(fmt.Sprintf("MCP server stopped unexpectedly: %v", err))
	})
	return controller, nil
}

func (controller *Controller) StartFromStore() {
	if controller == nil || controller.server == nil {
		return
	}
	config := controller.server.Config()
	if !config.Enabled {
		controller.logInfo("MCP server disabled by saved configuration")
		return
	}
	prepared, err := ensureToken(config)
	if err != nil {
		controller.logError("prepare MCP authentication: " + err.Error())
		return
	}
	if err := controller.persist(prepared); err != nil {
		controller.logError("persist MCP configuration: " + err.Error())
		return
	}
	if err := controller.server.Apply(); err != nil {
		controller.logError("start MCP server: " + err.Error())
		return
	}
	controller.logInfo("MCP server listening at " + controller.server.Status().URL)
}

func (controller *Controller) Stop() error {
	if controller == nil || controller.server == nil || !controller.server.Status().Listening {
		return nil
	}
	if err := controller.server.Stop(); err != nil {
		controller.logError("stop MCP server: " + err.Error())
		return err
	}
	controller.logInfo("MCP server stopped")
	return nil
}

func (controller *Controller) Status() Status {
	if controller == nil || controller.server == nil {
		return Status{Port: DefaultPort}
	}
	return controller.server.Status()
}

func (controller *Controller) SetEnabled(enabled bool) error {
	if controller == nil || controller.server == nil {
		return errors.New("MCP server unavailable")
	}
	config := controller.server.Config()
	config.Enabled = enabled
	prepared, err := ensureToken(config)
	if err != nil {
		return err
	}
	if err := controller.persist(prepared); err != nil {
		return err
	}
	if err := controller.server.SetEnabled(enabled); err != nil {
		return err
	}
	controller.logInfo(fmt.Sprintf("MCP server enabled=%t", enabled))
	return nil
}

func (controller *Controller) SetPort(port int) error {
	if controller == nil || controller.server == nil {
		return errors.New("MCP server unavailable")
	}
	config := controller.server.Config()
	config.Port = port
	normalized, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	// Save before mutating the listener, but do not Configure here: Configure
	// would make Server.SetPort believe the new port was already active.
	if err := controller.store.Save(normalized); err != nil {
		return err
	}
	return controller.server.SetPort(port)
}

func (controller *Controller) SetTokenEnabled(enabled bool) error {
	if controller == nil || controller.server == nil {
		return errors.New("MCP server unavailable")
	}
	config := controller.server.Config()
	config.TokenEnabled = enabled
	prepared, err := ensureToken(config)
	if err != nil {
		return err
	}
	if err := controller.persist(prepared); err != nil {
		return err
	}
	return controller.server.SetTokenEnabled(enabled)
}

func (controller *Controller) RegenerateToken() (string, error) {
	if controller == nil || controller.server == nil {
		return "", errors.New("MCP server unavailable")
	}
	config := controller.server.Config()
	if !config.TokenEnabled {
		return "", errors.New("enable MCP token authentication first")
	}
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}
	config.Token = token
	if err := controller.persist(config); err != nil {
		return "", err
	}
	if err := controller.server.SetToken(token); err != nil {
		return "", err
	}
	controller.logInfo("MCP authentication token regenerated")
	return token, nil
}

func (controller *Controller) InstallClient(client string) (InstallResult, error) {
	if controller == nil || controller.server == nil {
		return InstallResult{}, errors.New("MCP server unavailable")
	}
	status := controller.server.Status()
	if !status.Enabled || !status.Listening {
		if err := controller.SetEnabled(true); err != nil {
			return InstallResult{}, err
		}
		status = controller.server.Status()
	}
	if status.URL == "" || (status.TokenEnabled && status.Token == "") {
		return InstallResult{}, errors.New("MCP server authentication is not ready")
	}
	token := ""
	if status.TokenEnabled {
		token = status.Token
	}
	return InstallClientConfig(client, status.URL, token)
}

func (controller *Controller) persist(config Config) error {
	if controller.store == nil {
		return errors.New("MCP settings store unavailable")
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return err
	}
	if err := controller.store.Save(normalized); err != nil {
		return err
	}
	controller.server.Configure(normalized)
	return nil
}

func ensureToken(config Config) (Config, error) {
	if !config.TokenEnabled || config.Token != "" {
		return normalizeConfig(config)
	}
	token, err := GenerateToken()
	if err != nil {
		return Config{}, err
	}
	config.Token = token
	return normalizeConfig(config)
}

func (controller *Controller) logInfo(message string) {
	if controller != nil && controller.log != nil {
		controller.log.Info(message)
	}
}

func (controller *Controller) logError(message string) {
	if controller != nil && controller.log != nil {
		controller.log.Error(message)
	}
}
