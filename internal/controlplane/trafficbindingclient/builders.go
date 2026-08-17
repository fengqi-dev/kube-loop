package trafficbindingclient

import (
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
)

func NewInterceptBinding(
	mode trafficv1alpha1.TrafficBindingMode,
	owner Owner,
	snapshot servicebinding.ServiceInterceptSnapshot,
) (*trafficv1alpha1.TrafficBinding, error) {
	if mode != trafficv1alpha1.TrafficBindingModeExchange && mode != trafficv1alpha1.TrafficBindingModeMirror {
		return nil, fmt.Errorf("unsupported intercept TrafficBinding mode %q", mode)
	}
	ports := make([]trafficv1alpha1.TrafficPort, 0, len(snapshot.Ports))
	for _, port := range snapshot.Ports {
		relayPort := port.ListenPort
		ports = append(ports, trafficv1alpha1.TrafficPort{
			Name: port.Name, TargetPort: port.ServicePort, RelayPort: &relayPort,
			Protocol: trafficv1alpha1.TransportProtocol(strings.ToUpper(string(port.Protocol))),
		})
	}
	return &trafficv1alpha1.TrafficBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: snapshot.Namespace},
		Spec: trafficv1alpha1.TrafficBindingSpec{
			Mode: mode, SessionID: owner.SessionID, TaskID: owner.TaskID,
			SessionGeneration: owner.SessionGeneration,
			Target:            &trafficv1alpha1.TrafficTarget{Kind: trafficv1alpha1.TargetKindService, Name: snapshot.Service},
			Relay:             &trafficv1alpha1.RelayEndpoint{Address: snapshot.GatewayIP},
			Ports:             ports,
		},
	}, nil
}

func NewPreviewBinding(
	owner Owner,
	snapshot servicebinding.PreviewServiceSnapshot,
) *trafficv1alpha1.TrafficBinding {
	ports := make([]trafficv1alpha1.TrafficPort, 0, len(snapshot.Ports))
	for _, port := range snapshot.Ports {
		relayPort := port.ListenPort
		ports = append(ports, trafficv1alpha1.TrafficPort{
			Name: port.Name, TargetPort: port.ServicePort, RelayPort: &relayPort,
			Protocol: trafficv1alpha1.TransportProtocol(strings.ToUpper(string(port.Protocol))),
		})
	}
	return &trafficv1alpha1.TrafficBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: snapshot.Namespace},
		Spec: trafficv1alpha1.TrafficBindingSpec{
			Mode:      trafficv1alpha1.TrafficBindingModePreview,
			SessionID: owner.SessionID, TaskID: owner.TaskID, SessionGeneration: owner.SessionGeneration,
			Relay:   &trafficv1alpha1.RelayEndpoint{Address: snapshot.GatewayIP},
			Preview: &trafficv1alpha1.PreviewExposure{ServiceName: snapshot.Service},
			Ports:   ports,
		},
	}
}

func NewPortForwardBinding(
	owner Owner,
	namespace, kind, name, protocol string,
	port int32,
) *trafficv1alpha1.TrafficBinding {
	targetKind := trafficv1alpha1.TargetKindService
	if strings.EqualFold(kind, "pod") {
		targetKind = trafficv1alpha1.TargetKindPod
	}
	return &trafficv1alpha1.TrafficBinding{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace},
		Spec: trafficv1alpha1.TrafficBindingSpec{
			Mode:      trafficv1alpha1.TrafficBindingModePortForward,
			SessionID: owner.SessionID, TaskID: owner.TaskID, SessionGeneration: owner.SessionGeneration,
			Target: &trafficv1alpha1.TrafficTarget{
				Kind: targetKind,
				Name: name,
			},
			Ports: []trafficv1alpha1.TrafficPort{{
				TargetPort: port,
				Protocol:   trafficv1alpha1.TransportProtocol(strings.ToUpper(protocol)),
			}},
		},
	}
}
