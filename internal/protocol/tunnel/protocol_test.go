package tunnel

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

var protocolTestToken = SessionToken{1, 2, 3, 4}

func TestRelaySessionTokenIsStableAndDomainSeparated(t *testing.T) {
	first, err := RelaySessionToken("33333333-3333-4333-8333-333333333333", 7)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RelaySessionToken("33333333-3333-4333-8333-333333333333", 7)
	if err != nil {
		t.Fatal(err)
	}
	other, err := RelaySessionToken("44444444-4444-4444-8444-444444444444", 7)
	if err != nil {
		t.Fatal(err)
	}
	newGeneration, err := RelaySessionToken("33333333-3333-4333-8333-333333333333", 8)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first == other || first == newGeneration || first == (SessionToken{}) {
		t.Fatalf("first = %x, second = %x, other = %x", first, second, other)
	}
	if _, err := RelaySessionToken("not-a-uuid", 7); err == nil {
		t.Fatal("invalid session ID was accepted")
	}
	if _, err := RelaySessionToken("33333333-3333-4333-8333-333333333333", 0); err == nil {
		t.Fatal("zero Session generation was accepted")
	}
}

func TestOpenRoundTrip(t *testing.T) {
	var stream bytes.Buffer
	want := OpenRequest{Command: CommandTCP, Host: "api.default.svc.cluster.local", Port: 8080}
	if err := WriteOpen(&stream, want, protocolTestToken); err != nil {
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
	if err := WriteAccept(&stream, 99, protocolTestToken); err != nil {
		t.Fatal(err)
	}
	header, err := ReadSessionHeader(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if header.Command != CommandAccept {
		t.Fatalf("command=%d", header.Command)
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
	if err := WriteControlSession(&stream, protocolTestToken); err != nil {
		t.Fatal(err)
	}
	header, err := ReadSessionHeader(&stream)
	if err != nil {
		t.Fatal(err)
	}
	if header.Command != CommandControl {
		t.Fatalf("command=%d", header.Command)
	}
	if header.Token != protocolTestToken {
		t.Fatalf("token=%x", header.Token)
	}
}

func TestAuthorizedControlSessionRoundTrip(t *testing.T) {
	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: []string{"10.2.0.0/16"}, ServiceCIDRs: []string{"10.96.0.0/12"},
		ServiceIPs: []string{"10.96.0.10"}, DNSServer: "10.96.0.10",
	})
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	if err := WriteAuthorizedControlSession(&stream, protocolTestToken, spec); err != nil {
		t.Fatal(err)
	}
	header, err := ReadSessionHeader(&stream)
	if err != nil || header.Command != CommandControl || header.Token != protocolTestToken {
		t.Fatalf("header = %#v, %v", header, err)
	}
	decoded, err := ReadAuthorizedControlSpec(&stream)
	if err != nil {
		t.Fatal(err)
	}
	wantHash, _ := networkspec.Hash(spec)
	gotHash, _ := networkspec.Hash(decoded)
	if gotHash != wantHash {
		t.Fatalf("NetworkSpec hash = %q, want %q", gotHash, wantHash)
	}
}

func TestSessionHeaderRejectsZeroToken(t *testing.T) {
	wire := append(
		[]byte{'K', 'C', 'G', 2, CommandControl},
		make([]byte, len(SessionToken{}))...,
	)
	if _, err := ReadSessionHeader(bytes.NewReader(wire)); err == nil {
		t.Fatal("zero session token was accepted")
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
				return WriteOpen(
					w,
					OpenRequest{Command: CommandTCP, Host: "api", Port: 8080},
					protocolTestToken,
				)
			},
			want: append(
				sessionHeaderBytes(CommandTCP),
				0, 3, 'a', 'p', 'i', 0x1f, 0x90,
			),
		},
		{
			name: "control-session",
			write: func(w io.Writer) error {
				return WriteControlSession(w, protocolTestToken)
			},
			want: sessionHeaderBytes(CommandControl),
		},
		{
			name: "accept",
			write: func(w io.Writer) error {
				return WriteAccept(w, 0x0102030405060708, protocolTestToken)
			},
			want: append(sessionHeaderBytes(CommandAccept),
				1, 2, 3, 4, 5, 6, 7, 8,
			),
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
	open, err := ReadOpen(bytes.NewReader(append(
		sessionHeaderBytes(CommandUDP),
		0, 3, 'd', 'n', 's', 0, 53,
	)))
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

func sessionHeaderBytes(command byte) []byte {
	return append(
		[]byte{'K', 'C', 'G', 2, command},
		protocolTestToken[:]...,
	)
}
