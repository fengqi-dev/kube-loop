package protocol

type Frame interface {
	Encode(frame Frame) ([]byte, error)

	Decode(frame []byte) (Frame, error)
}
