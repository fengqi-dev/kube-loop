package intercept

import "testing"

func TestHostRouteRegistryInstallsLooksUpAndRemovesRoutes(t *testing.T) {
	registry := newHostRouteRegistry()
	service := &Service{
		Name: "API", Namespace: "Team", ClusterIP: "10.96.0.8",
	}
	ports := []InterceptPort{
		{Protocol: ProtocolTCP, ServicePort: 80},
		{Protocol: ProtocolUDP, ServicePort: 53},
	}
	portKeys := map[string]PortMapping{
		"team/api:tcp:80": {
			ServicePort: 80, Protocol: "tcp", LocalHost: "127.0.0.2", LocalPort: 8080,
		},
		"team/api:udp:53": {
			ServicePort: 53, Protocol: "udp", LocalHost: "127.0.0.3", LocalPort: 5353,
		},
	}
	primaryAddrs := map[string]string{
		"team/api:tcp:80": "10.244.0.8:8080",
	}

	keys := registry.install(
		service, ports, portKeys, primaryAddrs, ModeMirror, false, "team/api",
	)
	if len(keys) != 8 {
		t.Fatalf("installed %d keys, want 8", len(keys))
	}

	route, ok := registry.lookup(" API.Team.SVC.Cluster.Local ", 80)
	if !ok {
		t.Fatal("DNS route was not normalized during lookup")
	}
	if route.mode != ModeMirror || route.preview ||
		route.local.LocalHost != "127.0.0.2" || route.local.LocalPort != 8080 ||
		route.primaryAddr != "10.244.0.8:8080" {
		t.Fatalf("unexpected TCP route: %#v", route)
	}

	udp, ok := registry.lookup("10.96.0.8", 53)
	if !ok || udp.local.LocalPort != 5353 || udp.primaryAddr != "" {
		t.Fatalf("unexpected UDP route: %#v, found=%v", udp, ok)
	}

	registry.remove(keys)
	if _, ok := registry.lookup("api.team", 80); ok {
		t.Fatal("route remains after removal")
	}
}

func TestHostRouteRegistrySkipsServicesWithoutRewriteHosts(t *testing.T) {
	registry := newHostRouteRegistry()
	keys := registry.install(
		&Service{ClusterIP: "None"},
		[]InterceptPort{{Protocol: ProtocolTCP, ServicePort: 80}},
		nil,
		nil,
		"",
		false,
		"default/headless",
	)
	if len(keys) != 0 || len(registry.byTarget) != 0 {
		t.Fatalf("headless unnamed service installed routes: %#v", registry.byTarget)
	}
}
