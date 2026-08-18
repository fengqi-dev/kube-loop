//go:build darwin

package supervisor

import (
	"bytes"
	"net"
	"os"
	"strings"
	"testing"

	supervisorprotocol "github.com/fengqi-dev/kube-loop/internal/protocol/supervisor"
)

func TestClientReturnsEarlySupervisorRejection(t *testing.T) {
	t.Parallel()
	socket, err := os.CreateTemp("/tmp", "kubeloop-supervisor-*.sock")
	if err != nil {
		t.Fatal(err)
	}
	path := socket.Name()
	if err := socket.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = listener.Close()
		_ = os.Remove(path)
	})

	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.AcceptUnix()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		var request supervisorprotocol.Request
		if readErr := supervisorprotocol.ReadFrame(connection, &request, supervisorprotocol.MaxRequestBytes); readErr != nil {
			serverDone <- readErr
			return
		}
		serverDone <- supervisorprotocol.WriteFrame(connection, supervisorprotocol.Response{
			Protocol: supervisorprotocol.Version,
			Channel:  "dev",
			Error:    "worker is not installed",
		}, supervisorprotocol.MaxResponseBytes)
	}()

	client := &Client{Config: Config{SocketPath: path}, Token: "secret"}
	_, err = client.roundTrip(t.Context(), supervisorprotocol.Request{
		Protocol: supervisorprotocol.Version,
		Op:       supervisorprotocol.OpUpdateWorker,
	}, bytes.NewReader(make([]byte, 16<<20)))
	if err == nil || !strings.Contains(err.Error(), "worker is not installed") {
		t.Fatalf("roundTrip() error = %v, want supervisor rejection", err)
	}
	if strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "write worker payload") {
		t.Fatalf("roundTrip() exposed transport error instead of supervisor rejection: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("server: %v", err)
	}
}

func TestSupervisorResponseError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		response supervisorprotocol.Response
		want     string
	}{
		{name: "success", response: supervisorprotocol.Response{OK: true}},
		{name: "server message", response: supervisorprotocol.Response{Error: "rejected"}, want: "rejected"},
		{name: "missing server message", response: supervisorprotocol.Response{}, want: "supervisor request failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := supervisorResponseError(test.response)
			if test.want == "" {
				if err != nil {
					t.Fatalf("supervisorResponseError() = %v, want nil", err)
				}
				return
			}
			if err == nil || err.Error() != test.want {
				t.Fatalf("supervisorResponseError() = %v, want %q", err, test.want)
			}
		})
	}
}
