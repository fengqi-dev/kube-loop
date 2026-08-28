package relayticket

import (
	"errors"
	"time"
)

const (
	Version            = 2
	Type               = "KubeLoop-RelayTicket"
	Algorithm          = "EdDSA"
	DefaultLifetime    = time.Minute
	MaximumLifetime    = 2 * time.Minute
	DefaultClockSkew   = 15 * time.Second
	MaximumTicketBytes = 8 << 10
)

var ErrInvalid = errors.New("invalid relay ticket")

type Claims struct {
	Version           int      `json:"ver"`
	Issuer            string   `json:"iss"`
	Audience          string   `json:"aud"`
	IdentityID        string   `json:"sub"`
	Groups            []string `json:"groups,omitempty"`
	DeviceID          string   `json:"device_id"`
	SessionID         string   `json:"session_id"`
	SessionGeneration uint64   `json:"session_generation"`
	Namespace         string   `json:"namespace"`
	Operations        []string `json:"operations"`
	NetworkSpecHash   string   `json:"network_spec_hash"`
	TicketID          string   `json:"jti"`
	IssuedAt          int64    `json:"iat"`
	NotBefore         int64    `json:"nbf"`
	ExpiresAt         int64    `json:"exp"`
	TrafficEncryption *bool    `json:"traffic_encryption,omitempty"`
	NoisePublicKey    string   `json:"noise_public_key,omitempty"`
}

type header struct {
	Algorithm string `json:"alg"`
	Type      string `json:"typ"`
	KeyID     string `json:"kid"`
}
