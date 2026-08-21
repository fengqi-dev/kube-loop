package podssh

import (
	"errors"
	"io"
	"testing"
)

func checkTestClose(t testing.TB, closeResource func() error) {
	t.Helper()
	if err := closeResource(); err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("close test resource: %v", err)
	}
}
