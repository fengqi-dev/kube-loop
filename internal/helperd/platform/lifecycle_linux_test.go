//go:build linux

package platform

import "testing"

func TestTunHostAddr(t *testing.T) {
	addr, err := tunHostAddr("198.19.0.1/30")
	if err != nil {
		t.Fatal(err)
	}
	if addr.String() != "198.19.0.1" {
		t.Fatalf("got %s", addr)
	}
	addr, err = tunHostAddr("10.1.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if addr.String() != "10.1.0.2" {
		t.Fatalf("got %s", addr)
	}
}
