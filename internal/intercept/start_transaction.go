package intercept

import (
	"fmt"
	"maps"
	"slices"
)

type controlRegistrar interface {
	register(interceptID string, network byte, listenPort uint16) error
	unregister(interceptID string) error
}

// startTransaction tracks the reversible work performed while starting an
// Exchange, Mirror, or Preview. Call rollback with defer and commit only after
// the runtime and host routes have been published.
type startTransaction struct {
	control       controlRegistrar
	portKeys      map[string]PortMapping
	registered    []string
	compensations []func()
	committed     bool
}

func newStartTransaction(control controlRegistrar) *startTransaction {
	return &startTransaction{
		control:  control,
		portKeys: make(map[string]PortMapping),
	}
}

func (t *startTransaction) registerPorts(
	interceptID string,
	ports []InterceptPort,
	locals []PortMapping,
) error {
	for _, port := range ports {
		network := protocolToNetwork(port.Protocol)
		subID := fmt.Sprintf(
			"%s:%s:%d",
			interceptID,
			networkName(network),
			port.ServicePort,
		)
		if err := t.control.register(subID, network, uint16(port.ListenPort)); err != nil {
			return fmt.Errorf("register %s: %w", subID, err)
		}
		t.registered = append(t.registered, subID)
		t.portKeys[subID] = localFor(port, locals)
	}
	return nil
}

func (t *startTransaction) compensate(action func()) {
	t.compensations = append(t.compensations, action)
}

func (t *startTransaction) commit() {
	t.committed = true
}

func (t *startTransaction) rollback() {
	if t.committed {
		return
	}
	for _, compensate := range slices.Backward(t.compensations) {
		compensate()
	}
	for _, subID := range slices.Backward(t.registered) {
		_ = t.control.unregister(subID)
	}
}

func unregisterPorts(control controlRegistrar, portKeys map[string]PortMapping) {
	if control == nil {
		return
	}
	for _, subID := range slices.Sorted(maps.Keys(portKeys)) {
		_ = control.unregister(subID)
	}
}
