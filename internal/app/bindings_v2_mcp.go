package app

import (
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/mcp"
)

func (a *App) GetMCPStatus() mcp.Status {
	if a.mcp == nil {
		return mcp.Status{Port: mcp.DefaultPort, Error: "V2 MCP is unavailable"}
	}
	return a.mcp.Status()
}

func (a *App) SetMCPEnabled(enabled bool) error {
	if a.mcp == nil {
		return errors.New("V2 MCP is unavailable")
	}
	return a.mcp.SetEnabled(enabled)
}

func (a *App) SetMCPPort(port int) error {
	if a.mcp == nil {
		return errors.New("V2 MCP is unavailable")
	}
	return a.mcp.SetPort(port)
}

func (a *App) SetMCPTokenEnabled(enabled bool) error {
	if a.mcp == nil {
		return errors.New("V2 MCP is unavailable")
	}
	return a.mcp.SetTokenEnabled(enabled)
}

func (a *App) RegenerateMCPToken() (string, error) {
	if a.mcp == nil {
		return "", errors.New("V2 MCP is unavailable")
	}
	return a.mcp.RegenerateToken()
}

func (a *App) InstallMCPClient(client string) (mcp.InstallResult, error) {
	if a.mcp == nil {
		return mcp.InstallResult{}, errors.New("V2 MCP is unavailable")
	}
	return a.mcp.InstallClient(client)
}
