package gateway

import (
	"bufio"
	"net"
	"sync"

	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/transport/streamcopy"
)

func validNetworkSpecHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Server) relayUDP(client, target net.Conn) {
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = target.Close()
			_ = client.Close()
		})
	}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		defer stop()
		reader := bufio.NewReader(client)
		var buffer []byte
		for {
			payload, err := tunnel.ReadDatagram(reader, buffer)
			if err != nil {
				return
			}
			buffer = payload[:0]
			if _, err := target.Write(payload); err != nil {
				return
			}
		}
	}()

	buffer := make([]byte, tunnel.MaxDatagramSize)
	for {
		read, err := target.Read(buffer)
		if err != nil {
			stop()
			<-readerDone
			return
		}
		if err := tunnel.WriteDatagram(client, buffer[:read]); err != nil {
			stop()
			<-readerDone
			return
		}
	}
}

func relayTCP(left, right net.Conn) {
	streamcopy.Bidirectional(left, right)
}
