package dataplane

import "testing"

func checkTestClose(t testing.TB, closeResource func() error) {
	t.Helper()
	if err := closeResource(); err != nil {
		t.Errorf("close test resource: %v", err)
	}
}
