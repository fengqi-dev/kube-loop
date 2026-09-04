//go:build !windows && !darwin && !linux

package helperinstall

func prepareBinaryInstall() error {
	return nil
}
