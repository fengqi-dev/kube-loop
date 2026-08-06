package intercept

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

func TestControlSessionStateTransitions(t *testing.T) {
	session := newControlSession(nil)
	session.token = tunnel.SessionToken{1}
	first := &controlClient{}
	firstLost := make(chan struct{})
	session.attach(first, firstLost)

	if !session.ready() || session.current() != first || session.lostSignal() != firstLost {
		t.Fatal("attached control session is not ready")
	}
	snapshotClient, snapshotGeneration, ready := session.snapshot()
	if !ready || !session.matches(snapshotClient, snapshotGeneration) {
		t.Fatal("attached control session snapshot did not match")
	}
	old, generation := session.beginRecovery()
	if old != first {
		t.Fatalf("beginRecovery returned %#v, want first client", old)
	}
	if session.ready() || session.current() != nil || !session.recovering {
		t.Fatal("control session did not enter recovery state")
	}
	if session.matches(snapshotClient, snapshotGeneration) {
		t.Fatal("pre-recovery control snapshot still matched")
	}

	session.recoveryFailed(generation)
	if session.recovering {
		t.Fatal("failed recovery left session recovering")
	}

	second := &controlClient{}
	secondLost := make(chan struct{})
	session.attach(second, secondLost)
	if !session.ready() || session.lostSignal() != secondLost {
		t.Fatal("replacement control session is not ready")
	}
	if stopped := session.stop(); stopped != second {
		t.Fatalf("stop returned %#v, want replacement client", stopped)
	}
	if session.ready() || session.current() != nil || session.recovering {
		t.Fatal("stopped control session retained active state")
	}
}

func TestControlSessionRejectsStaleRecovery(t *testing.T) {
	session := newControlSession(nil)
	session.token = tunnel.SessionToken{1}
	session.attach(&controlClient{}, make(chan struct{}))
	_, generation := session.beginRecovery()
	session.stop()

	replacement := &controlClient{}
	replacementLost := make(chan struct{})
	session.attach(replacement, replacementLost)
	recovered := &controlClient{}
	if session.finishRecovery(generation, recovered, make(chan struct{})) {
		t.Fatal("stale recovery was accepted")
	}
	session.recoveryFailed(generation)
	if session.current() != replacement || session.lostSignal() != replacementLost {
		t.Fatal("stale recovery changed the replacement session")
	}
}

func TestControlSessionReportsImmediateConnectionLoss(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		header, err := tunnel.ReadSessionHeader(conn)
		if err != nil {
			serverErr <- err
			return
		}
		if header.Command != tunnel.CommandControl {
			serverErr <- fmt.Errorf("command = %d, want control", header.Command)
			return
		}
		serverErr <- tunnel.WriteStatus(conn, nil)
	}()

	session := newControlSession(nil)
	session.token = tunnel.SessionToken{1}
	if err := session.connect(context.Background(), listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.lostSignal():
	case <-time.After(3 * time.Second):
		t.Fatal("immediate control close did not signal loss")
	}
}

func TestControlSessionRedialRetriesStaleRegistration(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverErr := make(chan error, 1)
	release := make(chan struct{})
	go func() {
		for attempt := 1; attempt <= 2; attempt++ {
			conn, err := listener.Accept()
			if err != nil {
				serverErr <- err
				return
			}
			header, err := tunnel.ReadSessionHeader(conn)
			if err != nil {
				_ = conn.Close()
				serverErr <- err
				return
			}
			if header.Command != tunnel.CommandControl {
				_ = conn.Close()
				serverErr <- fmt.Errorf("command = %d, want control", header.Command)
				return
			}
			if err := tunnel.WriteStatus(conn, nil); err != nil {
				_ = conn.Close()
				serverErr <- err
				return
			}
			message, err := tunnel.ReadControlMessage(conn)
			if err != nil {
				_ = conn.Close()
				serverErr <- err
				return
			}
			if message.Type != tunnel.CtrlRegister {
				_ = conn.Close()
				serverErr <- fmt.Errorf("message type = %d, want register", message.Type)
				return
			}
			if attempt == 1 {
				err = tunnel.WriteControlMessage(conn, tunnel.ControlMessage{
					Type:  tunnel.CtrlError,
					Error: `intercept "default/api:tcp:80" already registered`,
				})
				_ = conn.Close()
				if err != nil {
					serverErr <- err
					return
				}
				continue
			}
			if err := tunnel.WriteControlMessage(conn, tunnel.ControlMessage{Type: tunnel.CtrlAck}); err != nil {
				_ = conn.Close()
				serverErr <- err
				return
			}
			<-release
			_ = conn.Close()
		}
		serverErr <- nil
	}()

	session := newControlSession(nil)
	session.token = tunnel.SessionToken{1}
	client, _, err := session.redial(context.Background(), listener.Addr().String(), []controlRegistration{{
		id:         "default/api:tcp:80",
		network:    tunnel.NetworkTCP,
		listenPort: 20001,
	}})
	if err != nil {
		close(release)
		t.Fatalf("redial: %v", err)
	}
	_ = client.close()
	close(release)
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
}
