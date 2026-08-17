package entity

import "time"

type Ticket struct {
	TokenType string
	Value     string
	ExpiresAt time.Time
	DeviceID  string
	RelayID   string
	Endpoint  string
}
