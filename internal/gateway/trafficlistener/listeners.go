package trafficlistener

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
)

type TCPBinding struct {
	Port     trafficmodel.Port
	Listener net.Listener
}

type UDPBinding struct {
	Port       trafficmodel.Port
	Connection *net.UDPConn
}

type Listeners struct {
	TCP      []TCPBinding
	UDP      []UDPBinding
	mappings []trafficcontrol.ListenerPort
	once     sync.Once
}

func Bind(gatewayIP string, ports []trafficmodel.Port) (*Listeners, error) {
	bound := &Listeners{
		TCP: make([]TCPBinding, 0, len(ports)), UDP: make([]UDPBinding, 0, len(ports)),
		mappings: make([]trafficcontrol.ListenerPort, 0, len(ports)),
	}
	fail := func(err error) (*Listeners, error) {
		_ = bound.Close()
		return nil, err
	}
	for _, port := range ports {
		var listenPort int
		switch port.Protocol {
		case "tcp":
			var listenConfig net.ListenConfig
			listener, err := listenConfig.Listen(
				context.Background(),
				"tcp",
				net.JoinHostPort(gatewayIP, "0"),
			)
			if err != nil {
				return fail(fmt.Errorf("listen for Service TCP port %d: %w", port.ServicePort, err))
			}
			address, ok := listener.Addr().(*net.TCPAddr)
			if !ok || address.Port == 0 {
				_ = listener.Close()
				return fail(errors.New("traffic TCP listener returned an invalid address"))
			}
			listenPort = address.Port
			bound.TCP = append(bound.TCP, TCPBinding{Port: port, Listener: listener})
		case "udp":
			address := &net.UDPAddr{IP: net.ParseIP(gatewayIP)}
			connection, err := net.ListenUDP("udp", address)
			if err != nil {
				return fail(fmt.Errorf("listen for Service UDP port %d: %w", port.ServicePort, err))
			}
			udpAddress, ok := connection.LocalAddr().(*net.UDPAddr)
			if !ok || udpAddress.Port == 0 {
				_ = connection.Close()
				return fail(errors.New("traffic UDP listener returned an invalid address"))
			}
			listenPort = udpAddress.Port
			bound.UDP = append(bound.UDP, UDPBinding{Port: port, Connection: connection})
		default:
			return fail(fmt.Errorf("unsupported traffic listener protocol %q", port.Protocol))
		}
		if listenPort < 1 || listenPort > 65535 {
			return fail(errors.New("traffic listener returned an invalid port"))
		}
		bound.mappings = append(bound.mappings, trafficcontrol.ListenerPort{
			Name: port.Name, Protocol: port.Protocol, ServicePort: port.ServicePort, ListenPort: int32(listenPort),
		})
	}
	return bound, nil
}

func (listeners *Listeners) Mappings() []trafficcontrol.ListenerPort {
	if listeners == nil {
		return nil
	}
	return append([]trafficcontrol.ListenerPort(nil), listeners.mappings...)
}

func (listeners *Listeners) Close() error {
	if listeners == nil {
		return nil
	}
	var closeErr error
	listeners.once.Do(func() {
		for _, binding := range listeners.TCP {
			if err := binding.Listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
		for _, binding := range listeners.UDP {
			if err := binding.Connection.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
				closeErr = errors.Join(closeErr, err)
			}
		}
	})
	return closeErr
}
