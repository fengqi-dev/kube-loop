package runtime

import (
	"net"
	"strconv"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

func TestAvailableTCPUDPPortSupportsBothProtocols(t *testing.T) {
	port, err := availableTCPUDPPort()
	if err != nil {
		t.Fatal(err)
	}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	tcpListener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("listen TCP on selected port: %v", err)
	}
	defer func() { _ = tcpListener.Close() }()
	udpListener, err := net.ListenPacket("udp", address)
	if err != nil {
		t.Fatalf("listen UDP on selected port: %v", err)
	}
	defer func() { _ = udpListener.Close() }()
}

func TestTrafficEndpointsShareListenAndDyeUsers(t *testing.T) {
	endpoints := trafficEndpoints(singbox.TrafficInboundPorts{Listen: 18081}, "password-32-chars-minimum-length!!")
	if err := endpoints.Validate(); err != nil {
		t.Fatal(err)
	}
	if endpoints.Exchange.Address != endpoints.Preview.Address {
		t.Fatalf("exchange/preview addresses differ: %q vs %q", endpoints.Exchange.Address, endpoints.Preview.Address)
	}
	if endpoints.Exchange.Username != singbox.TrafficUserExchange {
		t.Fatalf("exchange user = %q", endpoints.Exchange.Username)
	}
	if endpoints.Preview.Username != singbox.TrafficUserPreview {
		t.Fatalf("preview user = %q", endpoints.Preview.Username)
	}
	if endpoints.Exchange.Username == endpoints.Preview.Username {
		t.Fatal("exchange and preview must use distinct auth_user dyes")
	}
	if endpoints.MirrorShadow.Username != singbox.TrafficUserMirrorShadow {
		t.Fatalf("mirror-shadow user = %q", endpoints.MirrorShadow.Username)
	}
}
