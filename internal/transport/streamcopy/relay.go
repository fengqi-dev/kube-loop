package streamcopy

import (
	"errors"
	"io"
	"net"
	"time"
)

// ErrHalfCloseTimeout reports that the reverse direction did not finish within
// Options.HalfCloseTimeout after the first direction reached EOF.
var ErrHalfCloseTimeout = errors.New("relay half-close timeout")

// Side is one end of a relay. Reader and Writer are usually the same net.Conn,
// but some protocols hand over a buffered reader that is distinct from the
// writer -- the SOCKS bridge keeps reading through the handshake buffer -- so
// the two halves are kept separate.
type Side struct {
	Reader io.Reader
	Writer io.Writer
}

// ConnSide builds a Side whose halves are both conn.
func ConnSide(conn net.Conn) Side { return Side{Reader: conn, Writer: conn} }

// Options tunes a relay.
type Options struct {
	// HalfCloseTimeout bounds how long the relay waits for the second
	// direction once the first one has finished. A peer that half-closes but
	// never stops reading would otherwise hold the relay -- and the caller's
	// deferred cleanup -- open forever. Zero waits indefinitely, which keeps
	// half-close fully supported.
	HalfCloseTimeout time.Duration
}

// Result reports how much a relay carried and why it ended.
type Result struct {
	// LeftToRight counts the bytes copied from the left side to the right one.
	LeftToRight int64
	// RightToLeft counts the bytes copied in the reverse direction.
	RightToLeft int64
	// Err joins the failures of both directions, and carries
	// ErrHalfCloseTimeout when Options.HalfCloseTimeout expired. It is nil
	// when both directions ended at EOF.
	Err error
}

// Bidirectional copies both directions until each side has reached EOF. When
// one direction finishes, only the destination's write side is closed so the
// reverse direction can continue carrying a response.
func Bidirectional(left, right net.Conn) Result {
	return Relay(ConnSide(left), ConnSide(right), Options{})
}

// Relay copies both directions between two sides. It is the split-half form of
// Bidirectional, for callers whose read and write halves are distinct values.
//
// When Options.HalfCloseTimeout expires, both sides are closed outright to
// unblock the stalled direction and Relay returns without waiting for it, so
// that direction's byte count is reported as it stood when the relay started
// waiting.
func Relay(left, right Side, options Options) Result {
	done := make(chan outcome, 2)
	go func() { done <- pump(right.Writer, left.Reader, true) }()
	go func() { done <- pump(left.Writer, right.Reader, false) }()

	var result Result
	result.record(<-done)
	second, ok := await(done, options.HalfCloseTimeout)
	if !ok {
		forceClose(left, right)
		result.Err = errors.Join(result.Err, ErrHalfCloseTimeout)
		return result
	}
	result.record(second)
	return result
}

// outcome is one finished direction of a relay.
type outcome struct {
	written int64
	err     error
	toRight bool
}

// pump copies a single direction and then half-closes the destination so the
// peer observes EOF while the reverse direction stays open.
func pump(destination io.Writer, source io.Reader, toRight bool) outcome {
	written, err := io.Copy(destination, source)
	CloseWrite(destination)
	return outcome{written: written, err: err, toRight: toRight}
}

func (result *Result) record(finished outcome) {
	if finished.toRight {
		result.LeftToRight = finished.written
	} else {
		result.RightToLeft = finished.written
	}
	result.Err = errors.Join(result.Err, finished.err)
}

// await receives the second direction, giving up after timeout. A timeout of
// zero or less waits indefinitely.
func await(done <-chan outcome, timeout time.Duration) (outcome, bool) {
	if timeout <= 0 {
		return <-done, true
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case finished := <-done:
		return finished, true
	case <-timer.C:
		return outcome{}, false
	}
}

// forceClose closes every closable half of both sides. It is the escape hatch
// for a peer that half-closed but never finished reading: closing the
// underlying connection is what unblocks the stalled copy.
func forceClose(sides ...Side) {
	for _, side := range sides {
		closeValue(side.Reader)
		closeValue(side.Writer)
	}
}

func closeValue(value any) {
	if closer, ok := value.(io.Closer); ok {
		_ = closer.Close()
	}
}

// CloseWrite closes only the write side when supported. Closing the entire
// value is the fallback for transports that cannot represent a half-close; it
// also ensures the peer copy is not left blocked forever.
func CloseWrite(value any) {
	if writer, ok := value.(interface{ CloseWrite() error }); ok {
		_ = writer.CloseWrite()
		return
	}
	closeValue(value)
}
