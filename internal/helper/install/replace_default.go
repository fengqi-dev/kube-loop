//go:build !windows

package install

import "os"

func replaceFile(source, destination string) error {
	return os.Rename(source, destination)
}
