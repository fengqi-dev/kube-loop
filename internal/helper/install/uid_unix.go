//go:build unix

package install

import "os"

func currentUID() int { return os.Getuid() }
