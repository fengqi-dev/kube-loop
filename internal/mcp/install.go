package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/fsatomic"
	toml "github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

const clientServerName = "kubeloop"

// Supported MCP client identifiers for InstallClientConfig.
const (
	ClientClaude = "claude"
	ClientCodex  = "codex"
	ClientCursor = "cursor"
	ClientVSCode = "vscode"
)

// InstallResult describes a successful client config write.
type InstallResult struct {
	Client string `json:"client"`
	Path   string `json:"path"`
}

// ClientConfigPath returns the user-scoped MCP config path for a client.
func ClientConfigPath(client string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("find home directory: %w", err)
	}
	switch strings.ToLower(strings.TrimSpace(client)) {
	case ClientClaude:
		if dir := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR")); dir != "" {
			return filepath.Join(dir, ".claude.json"), nil
		}
		return filepath.Join(home, ".claude.json"), nil
	case ClientCodex:
		if dir := strings.TrimSpace(os.Getenv("CODEX_HOME")); dir != "" {
			return filepath.Join(dir, "config.toml"), nil
		}
		return filepath.Join(home, ".codex", "config.toml"), nil
	case ClientCursor:
		return filepath.Join(home, ".cursor", "mcp.json"), nil
	case ClientVSCode:
		return vscodeUserMCPPath(home)
	default:
		return "", fmt.Errorf("unsupported mcp client %q (use claude, codex, cursor, or vscode)", client)
	}
}

func vscodeUserMCPPath(home string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Code", "User", "mcp.json"), nil
	case "windows":
		if appData := os.Getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, "Code", "User", "mcp.json"), nil
		}
		return filepath.Join(home, "AppData", "Roaming", "Code", "User", "mcp.json"), nil
	default:
		return filepath.Join(home, ".config", "Code", "User", "mcp.json"), nil
	}
}

// InstallClientConfig merges the KubeLoop MCP HTTP endpoint into a client's
// user-scoped configuration file. token may be empty when Bearer auth is off.
func InstallClientConfig(client, url, token string) (InstallResult, error) {
	client = strings.ToLower(strings.TrimSpace(client))
	if url == "" {
		return InstallResult{}, fmt.Errorf("mcp url is required")
	}
	path, err := ClientConfigPath(client)
	if err != nil {
		return InstallResult{}, err
	}
	switch client {
	case ClientClaude:
		err = installJSONMCPServers(path, url, token, true)
	case ClientCursor:
		err = installJSONMCPServers(path, url, token, false)
	case ClientVSCode:
		err = installVSCodeMCP(path, url, token)
	case ClientCodex:
		err = installCodexTOML(path, url, token)
	default:
		return InstallResult{}, fmt.Errorf("unsupported mcp client %q", client)
	}
	if err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Client: client, Path: path}, nil
}

func installJSONMCPServers(path, url, token string, requireType bool) error {
	server := map[string]any{"url": url}
	if token != "" {
		server["headers"] = map[string]string{
			"Authorization": "Bearer " + token,
		}
	}
	if requireType {
		server["type"] = "http"
	}
	return upsertJSONMap(path, "mcpServers", clientServerName, server)
}

func installVSCodeMCP(path, url, token string) error {
	server := map[string]any{
		"type": "http",
		"url":  url,
	}
	if token != "" {
		server["headers"] = map[string]string{
			"Authorization": "Bearer " + token,
		}
	}
	return upsertJSONMap(path, "servers", clientServerName, server)
}

func upsertJSONMap(path, rootKey, serverName string, server map[string]any) error {
	raw, err := readOptionalFile(path)
	if err != nil {
		return err
	}
	root := map[string]any{}
	if len(bytes.TrimSpace(raw)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			return fmt.Errorf("decode %s: %w", path, err)
		}
	}
	servers, _ := root[rootKey].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[serverName] = server
	root[rootKey] = servers

	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	out = append(out, '\n')
	return fsatomic.WriteFile(path, out, 0o755, 0o600)
}

func installCodexTOML(path, url, token string) error {
	raw, err := readOptionalFile(path)
	if err != nil {
		return err
	}
	content, err := removeTOMLTable(raw, []string{"mcp_servers", clientServerName})
	if err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	server := codexMCPServer{URL: url}
	if token != "" {
		server.HTTPHeaders = map[string]string{"Authorization": "Bearer " + token}
	}
	block, err := toml.Marshal(codexMCPConfig{
		MCPServers: map[string]codexMCPServer{clientServerName: server},
	})
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	content = bytes.TrimRight(content, "\r\n")
	if len(content) > 0 {
		content = append(content, '\n')
	}
	content = append(content, block...)
	return fsatomic.WriteFile(path, content, 0o755, 0o600)
}

type codexMCPConfig struct {
	MCPServers map[string]codexMCPServer `toml:"mcp_servers"`
}

type codexMCPServer struct {
	URL         string            `toml:"url"`
	HTTPHeaders map[string]string `toml:"http_headers,omitempty"`
}

type byteRange struct {
	start int
	end   int
}

func removeTOMLTable(content []byte, prefix []string) ([]byte, error) {
	parser := unstable.Parser{}
	parser.Reset(content)
	var ranges []byteRange
	start := -1
	for parser.NextExpression() {
		expression := parser.Expression()
		if expression.Kind != unstable.Table && expression.Kind != unstable.ArrayTable {
			continue
		}
		key, offset := tomlExpressionKey(expression)
		lineStart := bytes.LastIndexByte(content[:offset], '\n') + 1
		if len(key) >= len(prefix) && slices.Equal(key[:len(prefix)], prefix) {
			if start < 0 {
				start = lineStart
			}
		} else if start >= 0 {
			ranges = append(ranges, byteRange{start: start, end: lineStart})
			start = -1
		}
	}
	if err := parser.Error(); err != nil {
		return nil, err
	}
	if start >= 0 {
		ranges = append(ranges, byteRange{start: start, end: len(content)})
	}
	if len(ranges) == 0 {
		return bytes.Clone(content), nil
	}
	out := make([]byte, 0, len(content))
	offset := 0
	for _, item := range ranges {
		out = append(out, content[offset:item.start]...)
		offset = item.end
	}
	return append(out, content[offset:]...), nil
}

func tomlExpressionKey(expression *unstable.Node) ([]string, int) {
	iterator := expression.Key()
	var key []string
	offset := 0
	for iterator.Next() {
		node := iterator.Node()
		if len(key) == 0 {
			offset = int(node.Raw.Offset)
		}
		key = append(key, string(node.Data))
	}
	return key, offset
}

func readOptionalFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return raw, nil
}
