package sshserver

import (
	"errors"
	"io"
	"net"
	"testing"
)

func checkTestClose(t testing.TB, closeResource func() error) {
	t.Helper()
	if err := closeResource(); err != nil &&
		!errors.Is(err, net.ErrClosed) && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("close test resource: %v", err)
	}
}
