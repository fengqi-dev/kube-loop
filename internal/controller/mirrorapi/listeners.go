package mirrorapi

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/servicebinding"
	corev1 "k8s.io/api/core/v1"
)

type tcpBinding struct {
	port     Port
	listener net.Listener
}

type udpBinding struct {
	port       Port
	connection *net.UDPConn
}

type boundListeners struct {
	tcp      []tcpBinding
	udp      []udpBinding
	mappings []servicebinding.InterceptPort
	once     sync.Once
}

func bindMirrorListeners(gatewayIP string, ports []Port) (*boundListeners, error) {
	bound := &boundListeners{
		tcp: make([]tcpBinding, 0, len(ports)), udp: make([]udpBinding, 0, len(ports)),
		mappings: make([]servicebinding.InterceptPort, 0, len(ports)),
	}
	fail := func(err error) (*boundListeners, error) {
		_ = bound.Close()
		return nil, err
	}
	for _, port := range ports {
		protocol := corev1.ProtocolTCP
		listenPort := 0
		switch port.Protocol {
		case "tcp":
			listener, err := net.Listen("tcp", net.JoinHostPort(gatewayIP, "0"))
			if err != nil {
				return fail(fmt.Errorf("listen for Service TCP port %d: %w", port.ServicePort, err))
			}
			address, ok := listener.Addr().(*net.TCPAddr)
			if !ok || address.Port == 0 {
				_ = listener.Close()
				return fail(errors.New("Mirror TCP listener returned an invalid address"))
			}
			listenPort = address.Port
			bound.tcp = append(bound.tcp, tcpBinding{port: port, listener: listener})
		case "udp":
			protocol = corev1.ProtocolUDP
			address := &net.UDPAddr{IP: net.ParseIP(gatewayIP)}
			connection, err := net.ListenUDP("udp", address)
			if err != nil {
				return fail(fmt.Errorf("listen for Service UDP port %d: %w", port.ServicePort, err))
			}
			listenPort = connection.LocalAddr().(*net.UDPAddr).Port
			bound.udp = append(bound.udp, udpBinding{port: port, connection: connection})
		default:
			return fail(fmt.Errorf("unsupported Mirror protocol %q", port.Protocol))
		}
		if listenPort < 1 || listenPort > 65535 {
			return fail(errors.New("Mirror listener returned an invalid port"))
		}
		bound.mappings = append(bound.mappings, servicebinding.InterceptPort{
			Name: port.Name, Protocol: protocol, ServicePort: port.ServicePort, ListenPort: int32(listenPort),
		})
	}
	return bound, nil
}

func (listeners *boundListeners) Close() error {
	if listeners == nil {
		return nil
	}
	var closeErr error
	listeners.once.Do(func() {
		for _, binding := range listeners.tcp {
			if err := binding.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		for _, binding := range listeners.udp {
			if err := binding.connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
	})
	return closeErr
}

func portAddress(ip string, port int32) string {
	return net.JoinHostPort(ip, strconv.Itoa(int(port)))
}
