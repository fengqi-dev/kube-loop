// Package streamcopy contains connection-copy helpers that preserve TCP
// half-close semantics across protocol adapters.
package streamcopy

import (
	"io"
	"net"
)

// Bidirectional copies both directions until each side has reached EOF. When
// one direction finishes, only the destination's write side is closed so the
// reverse direction can continue carrying a response.
func Bidirectional(left, right net.Conn) {
	done := make(chan struct{}, 2)
	copyOne := func(destination, source net.Conn) {
		_, _ = io.Copy(destination, source)
		CloseWrite(destination)
		done <- struct{}{}
	}
	go copyOne(left, right)
	go copyOne(right, left)
	<-done
	<-done
}

// CloseWrite closes only the write side when supported. Closing the entire
// value is the fallback for transports that cannot represent a half-close; it
// also ensures the peer copy is not left blocked forever.
func CloseWrite(value any) {
	if writer, ok := value.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
		return
	}
	if closer, ok := value.(io.Closer); ok {
		_ = closer.Close()
	}
}
