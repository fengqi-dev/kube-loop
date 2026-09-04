//go:build unix

package helperinstall

import "os"

func currentUID() int { return os.Getuid() }
