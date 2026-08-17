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
