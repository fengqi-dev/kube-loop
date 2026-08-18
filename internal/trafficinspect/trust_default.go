//go:build !darwin && !linux && !windows

package trafficinspect

import "context"

type unsupportedTrustStore struct{}

func newSystemTrustStore() TrustStore {
	return unsupportedTrustStore{}
}

func (unsupportedTrustStore) Status(context.Context, *Authority) (TrustStatus, error) {
	return TrustStatus{}, ErrSystemTrustUnsupported
}

func (unsupportedTrustStore) Install(context.Context, *Authority) error {
	return ErrSystemTrustUnsupported
}

func (unsupportedTrustStore) Uninstall(context.Context, *Authority) error {
	return ErrSystemTrustUnsupported
}
