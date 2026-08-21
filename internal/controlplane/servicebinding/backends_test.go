package servicebinding

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
)

func TestResolveSnapshotBackendsFromEndpointSlices(t *testing.T) {
	ready, notReady, terminating := true, false, true
	snapshot := ServiceInterceptSnapshot{
		Ports: []InterceptPort{
			{Name: "http", ServicePort: 80, Protocol: corev1.ProtocolTCP},
			{Name: "dns", ServicePort: 53, Protocol: corev1.ProtocolUDP},
		},
		HasEndpointSlices: true,
		EndpointSlices: []discoveryv1.EndpointSlice{
			{
				Endpoints: []discoveryv1.Endpoint{
					{
						Addresses:  []string{"10.244.0.12"},
						Conditions: discoveryv1.EndpointConditions{Ready: &ready},
					},
					{
						Addresses:  []string{"10.244.0.10"},
						Conditions: discoveryv1.EndpointConditions{Ready: &ready},
					},
					{
						Addresses:  []string{"10.244.0.11"},
						Conditions: discoveryv1.EndpointConditions{Ready: &notReady},
					},
					{
						Addresses: []string{"10.244.0.13"},
						Conditions: discoveryv1.EndpointConditions{
							Ready:       &ready,
							Terminating: &terminating,
						},
					},
				},
				Ports: []discoveryv1.EndpointPort{
					{Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080))},
					{Name: new("dns"), Protocol: new(corev1.ProtocolUDP), Port: new(int32(5353))},
				},
			},
		},
	}
	sets, err := ResolveSnapshotBackends(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(sets) != 2 || sets[0].ServicePort != 53 || sets[0].Targets[0].Port != 5353 ||
		len(
			sets[1].Targets,
		) != 2 || sets[1].Targets[0].Address != "10.244.0.10" || sets[1].Targets[0].Port != 8080 {
		t.Fatalf("resolved EndpointSlice backends=%#v", sets)
	}
}

func TestResolveSnapshotBackendsSupportsUnnamedTargetPortAndLegacyEndpoints(t *testing.T) {
	for _, test := range []struct {
		name     string
		snapshot ServiceInterceptSnapshot
	}{
		{
			name: "unnamed EndpointSlice targetPort differs from Service port",
			snapshot: ServiceInterceptSnapshot{
				Ports:             []InterceptPort{{ServicePort: 80, Protocol: corev1.ProtocolTCP}},
				HasEndpointSlices: true,
				EndpointSlices: []discoveryv1.EndpointSlice{{
					Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.244.1.2"}}},
					Ports:     []discoveryv1.EndpointPort{{Port: new(int32(8080))}},
				}},
			},
		},
		{
			name: "legacy Endpoints",
			snapshot: ServiceInterceptSnapshot{
				Ports:        []InterceptPort{{Name: "http", ServicePort: 80, Protocol: corev1.ProtocolTCP}},
				HasEndpoints: true,
				EndpointsSubsets: []corev1.EndpointSubset{{
					Addresses: []corev1.EndpointAddress{{IP: "10.244.2.3"}},
					Ports:     []corev1.EndpointPort{{Name: "http", Port: 8081, Protocol: corev1.ProtocolTCP}},
				}},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			sets, err := ResolveSnapshotBackends(test.snapshot)
			if err != nil || len(sets) != 1 || len(sets[0].Targets) != 1 ||
				sets[0].Targets[0].Port < 8080 {
				t.Fatalf("resolved backends=%#v err=%v", sets, err)
			}
		})
	}
}

func TestResolveSnapshotBackendsRejectsMissingReadyTarget(t *testing.T) {
	notReady := false
	_, err := ResolveSnapshotBackends(ServiceInterceptSnapshot{
		Ports: []InterceptPort{
			{Name: "http", ServicePort: 80, Protocol: corev1.ProtocolTCP},
		},
		HasEndpointSlices: true,
		EndpointSlices: []discoveryv1.EndpointSlice{{
			Endpoints: []discoveryv1.Endpoint{{
				Addresses: []string{
					"10.244.0.10",
				}, Conditions: discoveryv1.EndpointConditions{Ready: &notReady},
			}},
			Ports: []discoveryv1.EndpointPort{{
				Name: new("http"), Protocol: new(corev1.ProtocolTCP), Port: new(int32(8080)),
			}},
		}},
	})
	if err == nil {
		t.Fatal("snapshot without ready backend was accepted")
	}
}
