package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func (p *Process) Snapshot(ctx context.Context) (singbox.Metrics, error) {
	response, err := p.request(ctx, "/connections")
	if err != nil {
		return singbox.Metrics{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return singbox.Metrics{}, fmt.Errorf("sing-box connections API returned %s", response.Status)
	}
	var raw clashConnections
	decoder := json.NewDecoder(io.LimitReader(response.Body, 8<<20))
	if err := decoder.Decode(&raw); err != nil {
		return singbox.Metrics{}, fmt.Errorf("decode sing-box connections: %w", err)
	}
	return mapClashMetrics(raw), nil
}

type clashConnections struct {
	DownloadTotal int64             `json:"downloadTotal"`
	UploadTotal   int64             `json:"uploadTotal"`
	Memory        uint64            `json:"memory"`
	Connections   []clashConnection `json:"connections"`
}

type clashConnection struct {
	ID       string `json:"id"`
	Metadata struct {
		Network         string `json:"network"`
		SourceIP        string `json:"sourceIP"`
		SourcePort      string `json:"sourcePort"`
		DestinationIP   string `json:"destinationIP"`
		DestinationPort string `json:"destinationPort"`
		Host            string `json:"host"`
		Process         string `json:"process"`
		ProcessPath     string `json:"processPath"`
		Type            string `json:"type"`
		User            string `json:"user"`
	} `json:"metadata"`
	Upload   int64    `json:"upload"`
	Download int64    `json:"download"`
	Start    string   `json:"start"`
	Chains   []string `json:"chains"`
	Rule     string   `json:"rule"`
}

func mapClashMetrics(raw clashConnections) singbox.Metrics {
	connections := make([]singbox.Connection, 0, len(raw.Connections))
	for _, item := range raw.Connections {
		destination := item.Metadata.Host
		if destination == "" {
			destination = joinHostPort(item.Metadata.DestinationIP, item.Metadata.DestinationPort)
		} else if item.Metadata.DestinationPort != "" {
			destination = net.JoinHostPort(destination, item.Metadata.DestinationPort)
		}
		process := item.Metadata.Process
		if process == "" {
			process = processName(item.Metadata.ProcessPath)
		}
		outbound := singbox.DirectOutbound
		if len(item.Chains) > 0 {
			outbound = item.Chains[0]
		}
		inbound := inboundTag(item.Metadata.Type)
		feature := ""
		if inbound == singbox.TrafficInbound {
			feature = item.Metadata.User
		}
		connections = append(connections, singbox.Connection{
			ID:          item.ID,
			Network:     item.Metadata.Network,
			Source:      joinHostPort(item.Metadata.SourceIP, item.Metadata.SourcePort),
			Destination: destination,
			Process:     process,
			Upload:      item.Upload,
			Download:    item.Download,
			StartedAt:   item.Start,
			Inbound:     inbound,
			Feature:     feature,
			Outbound:    outbound,
			Rule:        item.Rule,
		})
	}
	if connections == nil {
		connections = []singbox.Connection{}
	}
	return singbox.Metrics{
		DownloadTotal:     raw.DownloadTotal,
		UploadTotal:       raw.UploadTotal,
		Memory:            raw.Memory,
		ActiveConnections: len(connections),
		Connections:       connections,
	}
}

func inboundTag(value string) string {
	if _, tag, found := strings.Cut(value, "/"); found {
		return tag
	}
	return value
}

// processName turns Clash API processPath into a short executable label.
// Paths may look like "/usr/bin/curl (alice)".
func processName(processPath string) string {
	processPath = strings.TrimSpace(processPath)
	if processPath == "" {
		return ""
	}
	if before, _, ok := strings.Cut(processPath, " ("); ok {
		processPath = before
	}
	return filepath.Base(processPath)
}

func joinHostPort(host, port string) string {
	if host == "" && port == "" {
		return ""
	}
	if port == "" {
		return host
	}
	return net.JoinHostPort(host, port)
}
