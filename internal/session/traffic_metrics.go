package session

import (
	"net"
	"sort"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
	"github.com/fengqi-dev/kube-loop/internal/traffic"
)

func trafficDialer(endpoint singbox.TrafficEndpoint) traffic.Dialer {
	return traffic.Dialer{Endpoint: traffic.Endpoint{
		Address: endpoint.Address, Username: endpoint.Username, Password: endpoint.Password,
	}}
}

func trackedTrafficDialer(
	endpoint singbox.TrafficEndpoint, feature string, tracker *traffic.Tracker,
) intercept.TrafficDialer {
	if endpoint.Address == "" {
		return nil
	}
	return traffic.TrackedDialer{
		Inner:   trafficDialer(endpoint),
		Feature: feature,
		Tracker: tracker,
	}
}

func trackedPortForwardDialer(
	endpoint singbox.TrafficEndpoint, tracker *traffic.Tracker,
) portfwd.TrafficDialer {
	if endpoint.Address == "" {
		return nil
	}
	return traffic.TrackedDialer{
		Inner:   trafficDialer(endpoint),
		Feature: singbox.TrafficUserPortForward,
		Tracker: tracker,
	}
}

// mergeTrafficTracker dyes clash traffic-in rows and injects Adapter-tracked
// connections that clash_api missed (short-lived or no metadata.user).
func mergeTrafficTracker(metrics singbox.Metrics, tracker *traffic.Tracker) singbox.Metrics {
	if tracker == nil {
		return metrics
	}
	live := tracker.Snapshot()
	if len(live) == 0 && len(metrics.Connections) == 0 {
		return metrics
	}
	seenPorts := make(map[string]struct{}, len(metrics.Connections))
	for i := range metrics.Connections {
		conn := &metrics.Connections[i]
		if conn.Inbound != singbox.TrafficInbound {
			continue
		}
		_, port, err := net.SplitHostPort(conn.Source)
		if err != nil {
			continue
		}
		seenPorts[port] = struct{}{}
		if feature := tracker.FeatureBySourcePort(port); feature != "" {
			conn.Feature = feature
		}
	}
	for _, item := range live {
		_, port, _ := net.SplitHostPort(item.Source)
		if port != "" {
			if _, ok := seenPorts[port]; ok {
				continue
			}
		}
		network := item.Network
		if network == "tcp4" || network == "tcp6" {
			network = "tcp"
		}
		if network == "udp4" || network == "udp6" {
			network = "udp"
		}
		metrics.Connections = append(metrics.Connections, singbox.Connection{
			ID:          "adapter-" + item.ID,
			Network:     network,
			Source:      item.Source,
			Destination: item.Destination,
			Process:     "KubeLoop",
			Upload:      item.Upload,
			Download:    item.Download,
			StartedAt:   item.StartedAt.Format(time.RFC3339Nano),
			Inbound:     singbox.TrafficInbound,
			Feature:     item.Feature,
			Outbound:    trafficOutboundForFeature(item.Feature),
		})
	}
	metrics.ActiveConnections = len(metrics.Connections)
	return metrics
}

func trafficOutboundForFeature(feature string) string {
	switch feature {
	case singbox.TrafficUserPortForward:
		return singbox.KubernetesOutbound
	case singbox.TrafficUserExchange, singbox.TrafficUserPreview, singbox.TrafficUserMirrorShadow:
		return singbox.LocalOutbound
	default:
		return singbox.DirectOutbound
	}
}

func (m *Manager) retainMetrics(metrics singbox.Metrics) *singbox.Metrics {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.recentConnections == nil {
		m.recentConnections = make(map[string]recentConnection)
	}
	for _, connection := range metrics.Connections {
		m.recentConnections[connection.ID] = recentConnection{
			connection: connection,
			lastSeen:   now,
		}
	}
	for id, item := range m.recentConnections {
		if now.Sub(item.lastSeen) > connectionRetainFor {
			delete(m.recentConnections, id)
		}
	}
	m.pruneRecentConnections(maxRetainedConnections)

	if len(m.recentConnections) == 0 {
		metrics.Connections = []singbox.Connection{}
		m.lastTraffic = nil
		return &metrics
	}
	live := make(map[string]singbox.Connection, len(metrics.Connections))
	for _, connection := range metrics.Connections {
		live[connection.ID] = connection
	}
	merged := make([]singbox.Connection, 0, len(m.recentConnections))
	for id, item := range m.recentConnections {
		if connection, ok := live[id]; ok {
			merged = append(merged, connection)
			continue
		}
		merged = append(merged, item.connection)
	}
	metrics.Connections = limitConnections(
		m.annotateSpeeds(merged, now),
		maxPublishedConnections,
	)
	return &metrics
}

func (m *Manager) pruneRecentConnections(limit int) {
	if limit <= 0 || len(m.recentConnections) <= limit {
		return
	}
	items := make([]recentConnection, 0, len(m.recentConnections))
	for _, item := range m.recentConnections {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool {
		return connectionRank(items[i].connection) > connectionRank(items[j].connection)
	})
	m.recentConnections = make(map[string]recentConnection, limit)
	for _, item := range items[:limit] {
		m.recentConnections[item.connection.ID] = item
	}
}

func limitConnections(connections []singbox.Connection, limit int) []singbox.Connection {
	if limit <= 0 || len(connections) <= limit {
		return connections
	}
	sort.SliceStable(connections, func(i, j int) bool {
		return connectionRank(connections[i]) > connectionRank(connections[j])
	})
	return connections[:limit]
}

func connectionRank(connection singbox.Connection) int64 {
	return connection.DownloadSpeed + connection.UploadSpeed + connection.Download + connection.Upload
}

func (m *Manager) annotateSpeeds(connections []singbox.Connection, now time.Time) []singbox.Connection {
	if m.lastTraffic == nil {
		m.lastTraffic = make(map[string]connectionTraffic)
	}
	next := make(map[string]connectionTraffic, len(connections))
	for i := range connections {
		connection := &connections[i]
		next[connection.ID] = connectionTraffic{
			upload:   connection.Upload,
			download: connection.Download,
			at:       now,
		}
		previous, ok := m.lastTraffic[connection.ID]
		if !ok {
			continue
		}
		elapsed := now.Sub(previous.at).Seconds()
		if elapsed <= 0 {
			continue
		}
		if connection.DownloadSpeed == 0 {
			speed := int64(float64(connection.Download-previous.download) / elapsed)
			if speed > 0 {
				connection.DownloadSpeed = speed
			}
		}
		if connection.UploadSpeed == 0 {
			speed := int64(float64(connection.Upload-previous.upload) / elapsed)
			if speed > 0 {
				connection.UploadSpeed = speed
			}
		}
	}
	m.lastTraffic = next
	return connections
}

func (m *Manager) clearRecentConnections() {
	m.mu.Lock()
	m.recentConnections = nil
	m.lastTraffic = nil
	m.mu.Unlock()
}
