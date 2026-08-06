package intercept

import (
	"errors"
	"reflect"
	"testing"

	"github.com/fengqi-dev/kube-loop/internal/tunnel"
)

type fakeControlRegistrar struct {
	failAt       int
	registers    []controlRegistration
	unregisters  []string
	registerCall int
}

func (f *fakeControlRegistrar) register(id string, network byte, listenPort uint16) error {
	f.registerCall++
	if f.failAt > 0 && f.registerCall == f.failAt {
		return errors.New("register failed")
	}
	f.registers = append(f.registers, controlRegistration{
		id: id, network: network, listenPort: listenPort,
	})
	return nil
}

func (f *fakeControlRegistrar) unregister(id string) error {
	f.unregisters = append(f.unregisters, id)
	return nil
}

func TestStartTransactionRollsBackPartialRegistrations(t *testing.T) {
	control := &fakeControlRegistrar{failAt: 2}
	transaction := newStartTransaction(control)
	ports := []InterceptPort{
		{Protocol: ProtocolTCP, ServicePort: 80, ListenPort: 20001},
		{Protocol: ProtocolUDP, ServicePort: 53, ListenPort: 20002},
	}
	locals := []PortMapping{
		{Protocol: "tcp", ServicePort: 80, LocalPort: 8080},
		{Protocol: "udp", ServicePort: 53, LocalPort: 5353},
	}

	if err := transaction.registerPorts("team/api", ports, locals); err == nil {
		t.Fatal("expected second registration to fail")
	}
	transaction.rollback()

	if len(control.registers) != 1 ||
		control.registers[0].id != "team/api:tcp:80" ||
		control.registers[0].network != tunnel.NetworkTCP {
		t.Fatalf("successful registrations = %#v", control.registers)
	}
	if !reflect.DeepEqual(control.unregisters, []string{"team/api:tcp:80"}) {
		t.Fatalf("unregisters = %v", control.unregisters)
	}
	if len(transaction.portKeys) != 1 ||
		transaction.portKeys["team/api:tcp:80"].LocalPort != 8080 {
		t.Fatalf("port keys = %#v", transaction.portKeys)
	}
}

func TestStartTransactionCompensatesInReverseOrder(t *testing.T) {
	control := &fakeControlRegistrar{}
	transaction := newStartTransaction(control)
	var order []string
	transaction.compensate(func() { order = append(order, "first") })
	transaction.compensate(func() { order = append(order, "second") })

	transaction.rollback()
	if !reflect.DeepEqual(order, []string{"second", "first"}) {
		t.Fatalf("compensation order = %v", order)
	}
}

func TestStartTransactionCommitSuppressesRollback(t *testing.T) {
	control := &fakeControlRegistrar{}
	transaction := newStartTransaction(control)
	called := false
	transaction.compensate(func() { called = true })
	transaction.registered = append(transaction.registered, "team/api:tcp:80")

	transaction.commit()
	transaction.rollback()
	if called || len(control.unregisters) != 0 {
		t.Fatal("committed transaction performed rollback")
	}
}

func TestUnregisterPortsUsesDeterministicOrder(t *testing.T) {
	control := &fakeControlRegistrar{}
	unregisterPorts(control, map[string]PortMapping{
		"team/api:udp:53": {},
		"team/api:tcp:80": {},
	})
	if !reflect.DeepEqual(control.unregisters, []string{
		"team/api:tcp:80",
		"team/api:udp:53",
	}) {
		t.Fatalf("unregisters = %v", control.unregisters)
	}
}
