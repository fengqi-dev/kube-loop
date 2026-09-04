package exchangestream

import "github.com/fengqi-dev/kube-loop/internal/protocol/streamframe"

// Frame types and limits. They mirror streamframe today; this contract owns
// them so it can diverge from the shared layout on its own schedule.
const (
	Ready      = streamframe.Ready
	Open       = streamframe.Open
	Data       = streamframe.Data
	CloseWrite = streamframe.CloseWrite
	Close      = streamframe.Close
	Datagram   = streamframe.Datagram
	Stop       = streamframe.Stop

	ProtocolTCP = streamframe.ProtocolTCP
	ProtocolUDP = streamframe.ProtocolUDP

	MaximumData  = streamframe.MaximumData
	MaximumError = streamframe.MaximumError
)

// Frame multiplexes reverse Service traffic over one authenticated WebSocket.
// ServicePort is the stable Service port selected by the user; the Control Plane's
// ephemeral listener port never crosses the desktop trust boundary.
type Frame streamframe.Frame

// codec names this contract in the errors it reports.
var codec = streamframe.Codec{Name: "exchange"}

func Encode(frame Frame) ([]byte, error) {
	return codec.Encode(streamframe.Frame(frame))
}

func Decode(encoded []byte) (Frame, error) {
	frame, err := codec.Decode(encoded)
	if err != nil {
		return Frame{}, err
	}
	return Frame(frame), nil
}
