package sessionapi

import "time"

// ActiveSession is the validated Session contract shared with authenticated APIs.
type ActiveSession struct {
	ID              string
	Namespace       string
	Generation      uint64
	ExpiresAt       time.Time
	NetworkSpecHash string
}

// AcceptsStreamGeneration reports whether a Gateway-authenticated stream can
// still act for the active Session. Heartbeats advance the stored generation
// before long-lived Gateway transports are replaced, so an older generation
// remains valid; a generation ahead of the Control Plane never does.
func AcceptsStreamGeneration(current, stream uint64) bool {
	return current != 0 && stream != 0 && stream <= current
}
