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

func TestNewSessionToken(t *testing.T) {
	token, err := NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == (SessionToken{}) {
		t.Fatal("NewSessionToken returned a zero token")
	}
}

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

func TestOpenRequestAddress(t *testing.T) {
	tests := []struct {
		name    string
		request OpenRequest
		want    string
	}{
		{name: "hostname", request: OpenRequest{Host: "api.default", Port: 8080}, want: "api.default:8080"},
		{name: "IPv4", request: OpenRequest{Host: "10.0.0.1", Port: 53}, want: "10.0.0.1:53"},
		{name: "IPv6", request: OpenRequest{Host: "2001:db8::1", Port: 443}, want: "[2001:db8::1]:443"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.request.Address(); got != test.want {
				t.Fatalf("Address() = %q, want %q", got, test.want)
			}
		})
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

func TestTrafficOpenRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		mode string
	}{
		{name: "exchange", mode: TrafficModeExchange},
		{name: "mirror", mode: TrafficModeMirror},
		{name: "preview", mode: TrafficModePreview},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stream bytes.Buffer
			want := TrafficOpenRequest{
				Mode: test.mode, TaskID: "12345678-1234-4234-8234-123456789abc",
			}
			if err := WriteTrafficOpen(&stream, want, protocolTestToken); err != nil {
				t.Fatal(err)
			}
			got, err := ReadTrafficOpen(&stream)
			if err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestTrafficOpenRejectsInvalidModeTaskIDAndBody(t *testing.T) {
	tests := []struct {
		name    string
		request TrafficOpenRequest
	}{
		{
			name: "unknown mode",
			request: TrafficOpenRequest{
				Mode: "capture", TaskID: "12345678-1234-4234-8234-123456789abc",
			},
		},
		{
			name:    "malformed task ID",
			request: TrafficOpenRequest{Mode: TrafficModeExchange, TaskID: "not-a-uuid"},
		},
		{
			name: "noncanonical task ID",
			request: TrafficOpenRequest{
				Mode: TrafficModeExchange, TaskID: "12345678-1234-4234-8234-123456789ABC",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := WriteTrafficOpen(io.Discard, test.request, protocolTestToken); err == nil {
				t.Fatalf("invalid request was accepted: %#v", test.request)
			}
		})
	}

	validTaskID := []byte("12345678-1234-4234-8234-123456789abc")
	if _, err := ReadTrafficOpenBody(bytes.NewReader(append([]byte{255}, validTaskID...))); err == nil {
		t.Fatal("unknown traffic mode was accepted")
	}
	if _, err := ReadTrafficOpenBody(bytes.NewReader([]byte{trafficModeExchange})); err == nil {
		t.Fatal("truncated traffic open body was accepted")
	}
	invalidTaskID := append([]byte{trafficModeExchange}, bytes.Repeat([]byte{'x'}, trafficTaskIDSize)...)
	if _, err := ReadTrafficOpenBody(bytes.NewReader(invalidTaskID)); err == nil {
		t.Fatal("malformed traffic Task ID was accepted")
	}
	wrongCommand := append(sessionHeaderBytes(CommandTCP), trafficModeExchange)
	wrongCommand = append(wrongCommand, validTaskID...)
	if _, err := ReadTrafficOpen(bytes.NewReader(wrongCommand)); err == nil {
		t.Fatal("non-traffic command was accepted")
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
			name: "traffic-open",
			write: func(w io.Writer) error {
				return WriteTrafficOpen(w, TrafficOpenRequest{
					Mode: TrafficModeMirror, TaskID: "12345678-1234-4234-8234-123456789abc",
				}, protocolTestToken)
			},
			want: append(
				sessionHeaderBytes(CommandTraffic),
				append([]byte{trafficModeMirror}, []byte("12345678-1234-4234-8234-123456789abc")...)...,
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
}

func sessionHeaderBytes(command byte) []byte {
	return append(
		[]byte{'K', 'C', 'G', 2, command},
		protocolTestToken[:]...,
	)
}
