//go:build !windows

package helperinstall

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
