package traffic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func writeRequest(writer io.Writer, command byte, address string) error {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("parse target address %q: %w", address, err)
	}
	parsedPort, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || (parsedPort == 0 && command != socksCommandUDPAssociate) {
		return fmt.Errorf("invalid target port %q", rawPort)
	}
	if host == "" {
		return errors.New("target host is required")
	}
	encoded, err := encodeAddress(host, uint16(parsedPort))
	if err != nil {
		return err
	}
	request := append([]byte{socksVersion, command, 0}, encoded...)
	return utils.WriteAll(writer, request)
}

func readReply(reader io.Reader) (string, error) {
	header := make([]byte, 3)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion {
		return "", fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	if header[1] != 0 {
		return "", fmt.Errorf("sOCKS reply status %d", header[1])
	}
	host, port, err := readAddress(reader)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func splitTarget(address string) (string, uint16, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return "", 0, fmt.Errorf("parse target address %q: %w", address, err)
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return "", 0, fmt.Errorf("invalid target port %q", rawPort)
	}
	if host == "" {
		return "", 0, errors.New("target host is required")
	}
	return host, uint16(port), nil
}

func encodeAddress(host string, port uint16) ([]byte, error) {
	ip := net.ParseIP(host)
	var encoded []byte
	switch {
	case ip != nil && ip.To4() != nil:
		encoded = append([]byte{socksAddressIPv4}, ip.To4()...)
	case ip != nil:
		encoded = append([]byte{socksAddressIPv6}, ip.To16()...)
	case len(host) > 0 && len(host) <= 255:
		//nolint:gosec // This case explicitly bounds the domain length to one byte.
		encoded = append([]byte{socksAddressDomain, byte(len(host))}, host...)
	default:
		return nil, fmt.Errorf("invalid SOCKS target host %q", host)
	}
	encoded = binary.BigEndian.AppendUint16(encoded, port)
	return encoded, nil
}

func readAddress(reader io.Reader) (string, uint16, error) {
	var kind [1]byte
	if _, err := io.ReadFull(reader, kind[:]); err != nil {
		return "", 0, err
	}
	var host string
	switch kind[0] {
	case socksAddressIPv4:
		value := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	case socksAddressIPv6:
		value := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = net.IP(value).String()
	case socksAddressDomain:
		var length [1]byte
		if _, err := io.ReadFull(reader, length[:]); err != nil {
			return "", 0, err
		}
		value := make([]byte, int(length[0]))
		if _, err := io.ReadFull(reader, value); err != nil {
			return "", 0, err
		}
		host = string(value)
	default:
		return "", 0, fmt.Errorf("unsupported SOCKS address type %d", kind[0])
	}
	var rawPort [2]byte
	if _, err := io.ReadFull(reader, rawPort[:]); err != nil {
		return "", 0, err
	}
	return host, binary.BigEndian.Uint16(rawPort[:]), nil
}
