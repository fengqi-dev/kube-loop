//go:build darwin

package supervisor

import (
	"bytes"
	"os"
)

func newBytesReader(raw []byte) *bytes.Reader { return bytes.NewReader(raw) }

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return err
	}
	if err := dir.Sync(); err != nil {
		_ = dir.Close()
		return err
	}
	return dir.Close()
}
