package trafficinspect

import (
	"context"
	"errors"
)

// ErrSystemTrustUnsupported reports that this platform POC has no trust-store adapter.
var ErrSystemTrustUnsupported = errors.New("system certificate trust automation is unsupported on this platform")

// TrustStatus describes whether the exact authority certificate is installed.
type TrustStatus struct {
	Installed         bool
	FingerprintSHA256 string
	Store             string
}

// TrustStore manages only the public half of a traffic inspection authority.
type TrustStore interface {
	Status(context.Context, *Authority) (TrustStatus, error)
	Install(context.Context, *Authority) error
	Uninstall(context.Context, *Authority) error
}

// NewSystemTrustStore returns the platform trust-store implementation.
func NewSystemTrustStore() TrustStore {
	return newSystemTrustStore()
}
