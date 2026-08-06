package intercept

import (
	"net"
	"testing"
	"time"
)

func TestPrimaryAddressFromEndpointSlice(t *testing.T) {
	address, err := primaryAddress(
		[]Backend{{
			Address: "10.244.0.8",
			Ports: []BackendPort{{
				Name: "http", Protocol: ProtocolTCP, Port: 8080,
			}},
		}},
		InterceptPort{
			Name: "http", Protocol: ProtocolTCP, ServicePort: 80,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if address != "10.244.0.8:8080" {
		t.Fatalf("address = %q", address)
	}
}

func TestPrimaryAddressMatchesPortName(t *testing.T) {
	addr, err := primaryAddress(
		[]Backend{{
			Address: "10.244.0.5",
			Ports: []BackendPort{{
				Name: "http", Port: 8080, Protocol: ProtocolTCP,
			}},
		}},
		InterceptPort{Name: "http", ServicePort: 80, Protocol: ProtocolTCP},
	)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.244.0.5:8080" {
		t.Fatalf("addr=%q", addr)
	}
}

func TestShadowWriterDoesNotBlockWhenLocalTargetStalls(t *testing.T) {
	local, stalledPeer := net.Pipe()
	defer stalledPeer.Close()
	writer := newShadowWriter(local)
	payload := make([]byte, 32<<10)
	started := time.Now()
	for range mirrorShadowQueueSize * 4 {
		if _, err := writer.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("shadow writes blocked primary path for %s", elapsed)
	}
}

func TestPrimaryAddressMatchesUDP(t *testing.T) {
	addr, err := primaryAddress(
		[]Backend{{
			Address: "10.244.0.5",
			Ports: []BackendPort{{
				Name: "dns", Port: 5353, Protocol: ProtocolUDP,
			}},
		}},
		InterceptPort{Name: "dns", ServicePort: 53, Protocol: ProtocolUDP},
	)
	if err != nil {
		t.Fatal(err)
	}
	if addr != "10.244.0.5:5353" {
		t.Fatalf("addr=%q", addr)
	}
}
