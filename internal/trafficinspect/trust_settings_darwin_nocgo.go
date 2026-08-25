//go:build darwin && !cgo

package trafficinspect

import "errors"

type darwinNativeTrustSettings struct{}

func newDarwinTrustSettings() darwinTrustSettings { return darwinNativeTrustSettings{} }

func (darwinNativeTrustSettings) Installed(*Authority) (bool, error) {
	return false, errors.New("macOS certificate trust requires cgo")
}

func (darwinNativeTrustSettings) Install(*Authority) error {
	return errors.New("macOS certificate trust requires cgo")
}

func (darwinNativeTrustSettings) Uninstall(*Authority) error {
	return errors.New("macOS certificate trust requires cgo")
}
