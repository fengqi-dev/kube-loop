package remote

import (
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

type Session struct {
	ID              string           `json:"id"`
	Namespace       string           `json:"namespace"`
	State           string           `json:"state"`
	Generation      uint64           `json:"generation"`
	CreatedAt       time.Time        `json:"createdAt"              ts_type:"string"`
	UpdatedAt       time.Time        `json:"updatedAt"              ts_type:"string"`
	LastHeartbeatAt time.Time        `json:"lastHeartbeatAt"        ts_type:"string"`
	ExpiresAt       time.Time        `json:"expiresAt"              ts_type:"string"`
	NetworkSpec     networkspec.Spec `json:"networkSpec"`
	NetworkSpecHash string           `json:"networkSpecHash"`
	Capabilities    *Capabilities    `json:"capabilities,omitempty"`
}

type SessionUpdate struct {
	ProfileID string
	Session   Session
}

type RelayTicket struct {
	TokenType         string    `json:"tokenType"`
	Ticket            string    `json:"ticket"`
	ExpiresAt         time.Time `json:"expiresAt"                   ts_type:"string"`
	DeviceID          string    `json:"deviceId"`
	RelayID           string    `json:"relayId,omitempty"`
	Endpoint          string    `json:"endpoint,omitempty"`
	TrafficEncryption *bool     `json:"trafficEncryption,omitempty"`
	NoisePublicKey    string    `json:"noisePublicKey,omitempty"`
}
