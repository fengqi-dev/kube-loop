package mirror

import (
	"io"
	"net"
	"strconv"
)

func localAddress(target LocalTarget) string {
	return net.JoinHostPort(target.LocalHost, strconv.Itoa(int(target.LocalPort)))
}

func writeLocal(connection net.Conn, payload []byte) error {
	for len(payload) > 0 {
		count, err := connection.Write(payload)
		if err != nil {
			return err
		}
		if count == 0 {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}
