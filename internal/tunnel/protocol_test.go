package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestOpenRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	want := OpenRequest{Command: CommandTCP, Host: "api.default.svc.cluster.local", Port: 8080}
	if err := WriteOpen(&stream, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOpen(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestStatusRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	if err := WriteStatus(&stream, errors.New("target denied")); err != nil {
		t.Fatal(err)
	}
	if err := ReadStatus(&stream); err == nil || err.Error() != "target denied" {
		t.Fatalf("unexpected status error: %v", err)
	}
}

func TestDatagramRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	want := []byte("dns query")
	if err := WriteDatagram(&stream, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadDatagram(bufio.NewReader(&stream), nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestControlMessageRoundTrip(t *testing.T) {
	cases := []ControlMessage{
		{Type: CtrlRegister, InterceptID: "default/http:tcp:80", Network: NetworkTCP, ListenPort: 20080},
		{Type: CtrlUnregister, InterceptID: "default/http:tcp:80"},
		{Type: CtrlInboundReady, InterceptID: "default/http:udp:53", Network: NetworkUDP, StreamID: 42},
		{Type: CtrlAck},
		{Type: CtrlError, Error: "listen failed"},
	}
	for _, want := range cases {
		var stream bytes.Buffer
		if err := WriteControlMessage(&stream, want); err != nil {
			t.Fatalf("write %#v: %v", want, err)
		}
		got, err := ReadControlMessage(&stream)
		if err != nil {
			t.Fatalf("read %#v: %v", want, err)
		}
		if got != want {
			t.Fatalf("got %#v, want %#v", got, want)
		}
	}
}

func TestAcceptRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	if err := WriteAccept(&stream, 99); err != nil {
		t.Fatal(err)
	}
	command, err := ReadSessionHeader(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if command != CommandAccept {
		t.Fatalf("command=%d", command)
	}
	streamID, err := ReadAcceptStreamID(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if streamID != 99 {
		t.Fatalf("streamID=%d", streamID)
	}
}

func TestControlSessionHeader(t *testing.T) {
	var stream bytes.Buffer
	if err := WriteControlSession(&stream); err != nil {
		t.Fatal(err)
	}
	command, err := ReadSessionHeader(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if command != CommandControl {
		t.Fatalf("command=%d", command)
	}
}

func TestProtocolGoldenEncoding(t *testing.T) {
	tests := []struct {
		name  string
		write func(io.Writer) error
		want  []byte
	}{
		{
			name: "open",
			write: func(w io.Writer) error {
				return WriteOpen(w, OpenRequest{Command: CommandTCP, Host: "api", Port: 8080})
			},
			want: []byte{'K', 'C', 'G', 1, CommandTCP, 0, 3, 'a', 'p', 'i', 0x1f, 0x90},
		},
		{
			name:  "control-session",
			write: WriteControlSession,
			want:  []byte{'K', 'C', 'G', 1, CommandControl},
		},
		{
			name: "accept",
			write: func(w io.Writer) error {
				return WriteAccept(w, 0x0102030405060708)
			},
			want: []byte{
				'K', 'C', 'G', 1, CommandAccept,
				1, 2, 3, 4, 5, 6, 7, 8,
			},
		},
		{
			name: "status-ok",
			write: func(w io.Writer) error {
				return WriteStatus(w, nil)
			},
			want: []byte{StatusOK},
		},
		{
			name: "status-error",
			write: func(w io.Writer) error {
				return WriteStatus(w, errors.New("bad"))
			},
			want: []byte{StatusError, 0, 3, 'b', 'a', 'd'},
		},
		{
			name: "datagram",
			write: func(w io.Writer) error {
				return WriteDatagram(w, []byte("dns"))
			},
			want: []byte{0, 3, 'd', 'n', 's'},
		},
		{
			name: "control-register",
			write: func(w io.Writer) error {
				return WriteControlMessage(w, ControlMessage{
					Type: CtrlRegister, InterceptID: "id", Network: NetworkTCP, ListenPort: 53,
				})
			},
			want: []byte{CtrlRegister, 0, 7, 0, 2, 'i', 'd', NetworkTCP, 0, 53},
		},
		{
			name: "control-inbound-ready",
			write: func(w io.Writer) error {
				return WriteControlMessage(w, ControlMessage{
					Type: CtrlInboundReady, InterceptID: "id",
					Network: NetworkUDP, StreamID: 0x0102030405060708,
				})
			},
			want: []byte{
				CtrlInboundReady, 0, 13,
				1, 2, 3, 4, 5, 6, 7, 8,
				0, 2, 'i', 'd', NetworkUDP,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stream bytes.Buffer
			if err := test.write(&stream); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(stream.Bytes(), test.want) {
				t.Fatalf("wire bytes = % x, want % x", stream.Bytes(), test.want)
			}
		})
	}
}

func TestProtocolGoldenDecoding(t *testing.T) {
	open, err := ReadOpen(bytes.NewReader([]byte{
		'K', 'C', 'G', 1, CommandUDP, 0, 3, 'd', 'n', 's', 0, 53,
	}))
	if err != nil {
		t.Fatal(err)
	}
	if open != (OpenRequest{Command: CommandUDP, Host: "dns", Port: 53}) {
		t.Fatalf("decoded open = %#v", open)
	}

	control, err := ReadControlMessage(bytes.NewReader([]byte{
		CtrlRegister, 0, 7, 0, 2, 'i', 'd', NetworkTCP, 0, 53,
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := ControlMessage{
		Type: CtrlRegister, InterceptID: "id", Network: NetworkTCP, ListenPort: 53,
	}
	if control != want {
		t.Fatalf("decoded control = %#v, want %#v", control, want)
	}
}
