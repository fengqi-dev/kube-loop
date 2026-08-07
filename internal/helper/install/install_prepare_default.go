//go:build !windows && !darwin && !linux

package install

func prepareBinaryInstall() error {
	return nil
}
