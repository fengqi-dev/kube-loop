package filetransfer

import (
	"errors"
	"net"
	"testing"
)

func checkTestClose(t testing.TB, closeResource func() error) {
	t.Helper()
	if err := closeResource(); err != nil && !errors.Is(err, net.ErrClosed) {
		t.Errorf("close test resource: %v", err)
	}
}
