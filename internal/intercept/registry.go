package intercept

import (
	"cmp"
	"maps"
	"slices"
)

// runtimeRegistry owns the active intercept indexes. Manager.mu protects every
// call; the registry deliberately has no second lock so lifecycle operations
// can update routes, control state, and indexes atomically.
type runtimeRegistry struct {
	byID            map[string]*runtimeIntercept
	byKey           map[string]string // namespace/service -> id
	starting        map[string]uint64
	nextReservation uint64
}

func newRuntimeRegistry() *runtimeRegistry {
	return &runtimeRegistry{
		byID:     make(map[string]*runtimeIntercept),
		byKey:    make(map[string]string),
		starting: make(map[string]uint64),
	}
}

func runtimeKey(namespace, service string) string {
	return namespace + "/" + service
}

func (r *runtimeRegistry) get(id string) *runtimeIntercept {
	return r.byID[id]
}

func (r *runtimeRegistry) getByKey(key string) *runtimeIntercept {
	return r.byID[r.byKey[key]]
}

func (r *runtimeRegistry) containsKey(key string) bool {
	_, exists := r.byKey[key]
	return exists
}

func (r *runtimeRegistry) reserve(key string) (uint64, bool) {
	if r.containsKey(key) {
		return 0, false
	}
	if _, exists := r.starting[key]; exists {
		return 0, false
	}
	r.nextReservation++
	reservation := r.nextReservation
	r.starting[key] = reservation
	return reservation, true
}

func (r *runtimeRegistry) reserved(key string, reservation uint64) bool {
	return r.starting[key] == reservation && reservation != 0
}

func (r *runtimeRegistry) release(key string, reservation uint64) {
	if r.reserved(key, reservation) {
		delete(r.starting, key)
	}
}

func (r *runtimeRegistry) add(runtime *runtimeIntercept) {
	r.byID[runtime.info.ID] = runtime
	r.byKey[runtimeKey(runtime.info.Namespace, runtime.info.Service)] = runtime.info.ID
}

func (r *runtimeRegistry) remove(id string) *runtimeIntercept {
	runtime := r.byID[id]
	if runtime == nil {
		return nil
	}
	delete(r.byID, id)
	delete(r.byKey, runtimeKey(runtime.info.Namespace, runtime.info.Service))
	return runtime
}

func (r *runtimeRegistry) ids() []string {
	return slices.Sorted(maps.Keys(r.byID))
}

func (r *runtimeRegistry) values() []*runtimeIntercept {
	ids := r.ids()
	items := make([]*runtimeIntercept, 0, len(ids))
	for _, id := range ids {
		items = append(items, r.byID[id])
	}
	return items
}

func (r *runtimeRegistry) listPreviews() []Info {
	items := make([]Info, 0, len(r.byID))
	for _, runtime := range r.values() {
		if runtime.info.Preview {
			items = append(items, runtime.info)
		}
	}
	return items
}

func (r *runtimeRegistry) listByMode(mode string) []Info {
	items := make([]Info, 0, len(r.byID))
	for _, runtime := range r.values() {
		if runtime.info.Preview {
			continue
		}
		itemMode := cmp.Or(runtime.info.Mode, ModeExchange)
		if itemMode == mode {
			items = append(items, runtime.info)
		}
	}
	return items
}

func (r *runtimeRegistry) registrations() []controlRegistration {
	var registrations []controlRegistration
	for _, runtime := range r.values() {
		for subID := range runtime.portKeys {
			network, listenPort, ok := registrationFromRuntime(runtime, subID)
			if !ok {
				continue
			}
			registrations = append(registrations, controlRegistration{
				id: subID, network: network, listenPort: listenPort,
			})
		}
	}
	slices.SortFunc(registrations, func(a, b controlRegistration) int {
		return cmp.Compare(a.id, b.id)
	})
	return registrations
}

func (r *runtimeRegistry) findPort(
	subID string,
) (local PortMapping, primaryAddr, mode string, preview, found bool) {
	for _, runtime := range r.byID {
		mapping, ok := runtime.portKeys[subID]
		if !ok {
			continue
		}
		mode = runtime.info.Mode
		if mode == "" {
			mode = ModeExchange
		}
		if runtime.primaryAddrs != nil {
			primaryAddr = runtime.primaryAddrs[subID]
		}
		return mapping, primaryAddr, mode, runtime.info.Preview, true
	}
	return PortMapping{}, "", ModeExchange, false, false
}
