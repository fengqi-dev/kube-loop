package ticketapi

import "time"

type issueRequest struct{}

type issueResponse struct {
	TokenType         string    `json:"tokenType"`
	Ticket            string    `json:"ticket"`
	ExpiresAt         time.Time `json:"expiresAt"`
	DeviceID          string    `json:"deviceId"`
	RelayID           string    `json:"relayId,omitempty"`
	Endpoint          string    `json:"endpoint,omitempty"`
	TrafficEncryption *bool     `json:"trafficEncryption,omitempty"`
	NoisePublicKey    string    `json:"noisePublicKey,omitempty"`
}
