package tui

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

const workspaceConfigVersion = 1

type workspaceConfig struct {
	Version int               `yaml:"version"`
	Aliases map[string]string `yaml:"aliases"`
	Hotkeys map[string]string `yaml:"hotkeys"`
}

func loadWorkspaceConfig(path string) (workspaceConfig, string) {
	config := workspaceConfig{Version: workspaceConfigVersion, Aliases: map[string]string{}, Hotkeys: map[string]string{}}
	if strings.TrimSpace(path) == "" {
		return config, "TUI config path is unavailable"
	}
	contents, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return config, ""
	}
	if err != nil {
		return config, fmt.Sprintf("read TUI config: %v", err)
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(contents)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&config); err != nil {
		return workspaceConfig{Version: workspaceConfigVersion, Aliases: map[string]string{}, Hotkeys: map[string]string{}}, fmt.Sprintf("decode TUI config: %v", err)
	}
	if config.Version != workspaceConfigVersion {
		return workspaceConfig{Version: workspaceConfigVersion, Aliases: map[string]string{}, Hotkeys: map[string]string{}}, fmt.Sprintf("unsupported TUI config version %d", config.Version)
	}
	if config.Aliases == nil {
		config.Aliases = map[string]string{}
	}
	if config.Hotkeys == nil {
		config.Hotkeys = map[string]string{}
	}
	warnings := validateWorkspaceConfig(&config)
	return config, strings.Join(warnings, "; ")
}

func validateWorkspaceConfig(config *workspaceConfig) []string {
	warnings := []string{}
	reserved := map[string]struct{}{":": {}, "/": {}, "?": {}, "q": {}, "esc": {}, "ctrl+c": {}}
	for _, descriptor := range workspaceResourceRegistry {
		reserved[string(descriptor.id)] = struct{}{}
		for _, alias := range descriptor.aliases {
			reserved[alias] = struct{}{}
		}
	}
	aliasNames := make([]string, 0, len(config.Aliases))
	for alias := range config.Aliases {
		aliasNames = append(aliasNames, alias)
	}
	sort.Strings(aliasNames)
	for _, rawAlias := range aliasNames {
		alias := strings.ToLower(strings.TrimSpace(rawAlias))
		target := strings.ToLower(strings.TrimSpace(config.Aliases[rawAlias]))
		if alias == "" || strings.ContainsAny(alias, " \t:/") {
			warnings = append(warnings, fmt.Sprintf("ignored invalid alias %q", rawAlias))
			delete(config.Aliases, rawAlias)
			continue
		}
		if _, exists := reserved[alias]; exists {
			warnings = append(warnings, fmt.Sprintf("ignored reserved alias %q", alias))
			delete(config.Aliases, rawAlias)
			continue
		}
		if _, ok := builtinWorkspaceResource(target); !ok {
			warnings = append(warnings, fmt.Sprintf("ignored alias %q with unknown target %q", alias, target))
			delete(config.Aliases, rawAlias)
			continue
		}
		if rawAlias != alias {
			delete(config.Aliases, rawAlias)
		}
		config.Aliases[alias] = target
	}
	for rawKey, rawCommand := range config.Hotkeys {
		key := strings.ToLower(strings.TrimSpace(rawKey))
		command := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(rawCommand, ":")))
		if _, exists := reserved[key]; key == "" || exists {
			warnings = append(warnings, fmt.Sprintf("ignored reserved hotkey %q", rawKey))
			delete(config.Hotkeys, rawKey)
			continue
		}
		if _, ok := resolveWorkspaceResource(command, *config); !ok {
			warnings = append(warnings, fmt.Sprintf("ignored hotkey %q with unknown command %q", key, command))
			delete(config.Hotkeys, rawKey)
			continue
		}
		if rawKey != key {
			delete(config.Hotkeys, rawKey)
		}
		config.Hotkeys[key] = command
	}
	return warnings
}
