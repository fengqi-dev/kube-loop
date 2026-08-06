package mcp

import (
	"encoding/json"
	"fmt"
)

func getSingBoxDNSConfig(backend Backend) (singBoxDNSConfigOut, error) {
	raw, err := backend.SingBoxConfig()
	if err != nil {
		return singBoxDNSConfigOut{}, err
	}
	var config struct {
		DNS map[string]any `json:"dns"`
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return singBoxDNSConfigOut{}, fmt.Errorf("decode sing-box config: %w", err)
	}
	if config.DNS == nil {
		return singBoxDNSConfigOut{}, fmt.Errorf("sing-box DNS config unavailable")
	}
	return singBoxDNSConfigOut{DNS: config.DNS}, nil
}
