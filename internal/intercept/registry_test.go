package intercept

import "testing"

func TestRuntimeRegistryIndexesAndListsDeterministically(t *testing.T) {
	registry := newRuntimeRegistry()
	registry.add(&runtimeIntercept{info: Info{
		ID: "team/zeta", Namespace: "team", Service: "zeta", Mode: ModeMirror,
	}})
	registry.add(&runtimeIntercept{info: Info{
		ID: "team/alpha", Namespace: "team", Service: "alpha",
	}})
	registry.add(&runtimeIntercept{info: Info{
		ID: "team/preview", Namespace: "team", Service: "preview", Preview: true,
	}})

	if got := registry.ids(); len(got) != 3 ||
		got[0] != "team/alpha" || got[1] != "team/preview" || got[2] != "team/zeta" {
		t.Fatalf("ids are not deterministic: %v", got)
	}
	if got := registry.listByMode(ModeExchange); len(got) != 1 || got[0].ID != "team/alpha" {
		t.Fatalf("exchange list = %#v", got)
	}
	if got := registry.listByMode(ModeMirror); len(got) != 1 || got[0].ID != "team/zeta" {
		t.Fatalf("mirror list = %#v", got)
	}
	if got := registry.listPreviews(); len(got) != 1 || got[0].ID != "team/preview" {
		t.Fatalf("preview list = %#v", got)
	}

	if !registry.containsKey("team/alpha") ||
		registry.getByKey("team/alpha").info.ID != "team/alpha" {
		t.Fatal("namespace/service index did not find alpha")
	}
	removed := registry.remove("team/alpha")
	if removed == nil || registry.containsKey("team/alpha") || registry.get("team/alpha") != nil {
		t.Fatalf("remove did not clear both indexes: %#v", removed)
	}
}

func TestRuntimeRegistryFindsPortsAndBuildsSortedRegistrations(t *testing.T) {
	registry := newRuntimeRegistry()
	tcpID := "team/api:tcp:80"
	udpID := "team/api:udp:53"
	registry.add(&runtimeIntercept{
		info: Info{
			ID: "team/api", Namespace: "team", Service: "api", Mode: ModeMirror,
			Ports: []InterceptPort{
				{Protocol: ProtocolUDP, ServicePort: 53, ListenPort: 20002},
				{Protocol: ProtocolTCP, ServicePort: 80, ListenPort: 20001},
			},
		},
		portKeys: map[string]PortMapping{
			udpID: {ServicePort: 53, Protocol: "udp", LocalPort: 5353},
			tcpID: {ServicePort: 80, Protocol: "tcp", LocalPort: 8080},
		},
		primaryAddrs: map[string]string{tcpID: "10.244.0.8:8080"},
	})

	local, primary, mode, preview, found := registry.findPort(tcpID)
	if !found || local.LocalPort != 8080 || primary != "10.244.0.8:8080" ||
		mode != ModeMirror || preview {
		t.Fatalf(
			"findPort = local=%#v primary=%q mode=%q preview=%v found=%v",
			local, primary, mode, preview, found,
		)
	}

	registrations := registry.registrations()
	if len(registrations) != 2 ||
		registrations[0].id != tcpID ||
		registrations[1].id != udpID {
		t.Fatalf("registrations are not sorted: %#v", registrations)
	}
}

func TestRuntimeRegistryReservationsRejectConcurrentAndStaleRelease(t *testing.T) {
	registry := newRuntimeRegistry()
	first, ok := registry.reserve("team/api")
	if !ok || first == 0 {
		t.Fatal("first reservation was rejected")
	}
	if _, ok := registry.reserve("team/api"); ok {
		t.Fatal("concurrent reservation was accepted")
	}

	registry.release("team/api", first+1)
	if !registry.reserved("team/api", first) {
		t.Fatal("stale release cleared current reservation")
	}
	registry.release("team/api", first)

	second, ok := registry.reserve("team/api")
	if !ok || second == first {
		t.Fatalf("second reservation = %d, first = %d", second, first)
	}
	registry.release("team/api", second)
	registry.add(&runtimeIntercept{info: Info{
		ID: "team/api", Namespace: "team", Service: "api",
	}})
	if _, ok := registry.reserve("team/api"); ok {
		t.Fatal("active runtime key was reserved")
	}
}
