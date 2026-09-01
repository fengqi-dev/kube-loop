package controller

import (
	"context"
	"testing"

	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

type staleEndpointSliceReadClient struct {
	client.Client

	key   types.NamespacedName
	stale bool
}

func (stale *staleEndpointSliceReadClient) Get(
	ctx context.Context,
	key client.ObjectKey,
	object client.Object,
	options ...client.GetOption,
) error {
	if stale.stale && key == stale.key {
		if _, ok := object.(*discoveryv1.EndpointSlice); ok {
			stale.stale = false
			return apierrors.NewNotFound(
				schema.GroupResource{Group: discoveryv1.GroupName, Resource: "endpointslices"},
				key.Name,
			)
		}
	}
	return stale.Client.Get(ctx, key, object, options...)
}

func TestRestoreEndpointSliceConvergesAfterCreateRace(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := discoveryv1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Namespace: "development", Name: "api-abcde"}
	oldPort := int32(8080)
	existing := &discoveryv1.EndpointSlice{
		AddressType: discoveryv1.AddressTypeIPv4,
		Endpoints:   []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.9"}}},
		Ports:       []discoveryv1.EndpointPort{{Port: &oldPort}},
	}
	existing.Name = key.Name
	existing.Namespace = key.Namespace
	base := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing).Build()
	reconciler := &TrafficBindingReconciler{Client: &staleEndpointSliceReadClient{
		Client: base, key: key, stale: true,
	}}
	wantedPort := int32(9090)
	err := reconciler.restoreEndpointSlice(
		context.Background(),
		key.Namespace,
		&trafficv1alpha1.EndpointSliceSnapshot{
			Name: key.Name, AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints: []discoveryv1.Endpoint{{Addresses: []string{"10.0.0.8"}}},
			Ports:     []discoveryv1.EndpointPort{{Port: &wantedPort}},
		},
	)
	if err != nil {
		t.Fatalf("restore EndpointSlice after create race: %v", err)
	}
	current := &discoveryv1.EndpointSlice{}
	if err := base.Get(context.Background(), key, current); err != nil {
		t.Fatal(err)
	}
	if got := current.Endpoints[0].Addresses[0]; got != "10.0.0.8" {
		t.Fatalf("restored address = %q", got)
	}
	if got := *current.Ports[0].Port; got != wantedPort {
		t.Fatalf("restored port = %d", got)
	}
}
