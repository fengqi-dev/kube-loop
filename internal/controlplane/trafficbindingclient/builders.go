package trafficbindingclient

import (
	"fmt"
	"strings"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
)

func NewPendingInterceptBinding(
	mode trafficv1alpha1.TrafficBindingMode,
	owner Owner,
	namespace, service, clusterIP string,
	ports []entity.Port,
	localTargets []entity.LocalTarget,
) (*trafficv1alpha1.TrafficBinding, error) {
	if mode != trafficv1alpha1.TrafficBindingModeExchange &&
		mode != trafficv1alpha1.TrafficBindingModeMirror {
		return nil, fmt.Errorf("unsupported intercept TrafficBinding mode %q", mode)
	}
	return pendingBinding(mode, owner, namespace, service, clusterIP, ports, localTargets), nil
}

func NewPendingPreviewBinding(
	owner Owner,
	namespace, service string,
	ports []entity.Port,
	localTargets []entity.LocalTarget,
) *trafficv1alpha1.TrafficBinding {
	binding := pendingBinding(
		trafficv1alpha1.TrafficBindingModePreview,
		owner, namespace, service, "", ports, localTargets,
	)
	binding.Spec.Target = nil
	binding.Spec.Preview = &trafficv1alpha1.PreviewExposure{ServiceName: service}
	return binding
}

func pendingBinding(
	mode trafficv1alpha1.TrafficBindingMode,
	owner Owner,
	namespace, service, clusterIP string,
	ports []entity.Port,
	localTargets []entity.LocalTarget,
) *trafficv1alpha1.TrafficBinding {
	targets := make(map[string]entity.LocalTarget, len(localTargets))
	for _, target := range localTargets {
		targets[strings.ToUpper(target.Protocol)+fmt.Sprintf("/%d", target.ServicePort)] = target
	}
	bindingPorts := make([]trafficv1alpha1.TrafficPort, 0, len(ports))
	for _, port := range ports {
		bindingPort := trafficv1alpha1.TrafficPort{
			Name: port.Name, TargetPort: port.ServicePort,
			Protocol: trafficv1alpha1.TransportProtocol(strings.ToUpper(port.Protocol)),
		}
		if target, ok := targets[strings.ToUpper(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)]; ok {
			localPort := int32(target.LocalPort)
			bindingPort.LocalHost, bindingPort.LocalPort = target.LocalHost, &localPort
		}
		bindingPorts = append(bindingPorts, bindingPort)
	}
	return &trafficv1alpha1.TrafficBinding{
		Namespace: namespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         mode, IdentityID: owner.IdentityID, SessionID: owner.SessionID,
			TaskID: owner.TaskID, SessionGeneration: owner.SessionGeneration,
			ClusterIP: clusterIP,
			Target: &trafficv1alpha1.TrafficTarget{
				Kind: trafficv1alpha1.TargetKindService, Name: service,
			},
			Ports: bindingPorts,
		},
	}
}

func NewInterceptBinding(
	mode trafficv1alpha1.TrafficBindingMode,
	owner Owner,
	snapshot servicebinding.ServiceInterceptSnapshot,
) (*trafficv1alpha1.TrafficBinding, error) {
	if mode != trafficv1alpha1.TrafficBindingModeExchange &&
		mode != trafficv1alpha1.TrafficBindingModeMirror {
		return nil, fmt.Errorf(
			"unsupported intercept TrafficBinding mode %q",
			mode,
		)
	}
	ports := make([]trafficv1alpha1.TrafficPort, 0, len(snapshot.Ports))
	for _, port := range snapshot.Ports {
		relayPort := port.ListenPort
		ports = append(ports, trafficv1alpha1.TrafficPort{
			Name: port.Name, TargetPort: port.ServicePort, RelayPort: &relayPort,
			Protocol: trafficv1alpha1.TransportProtocol(
				strings.ToUpper(string(port.Protocol)),
			),
		})
	}
	return &trafficv1alpha1.TrafficBinding{
		Namespace: snapshot.Namespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         mode, IdentityID: owner.IdentityID,
			SessionID: owner.SessionID, TaskID: owner.TaskID,
			SessionGeneration: owner.SessionGeneration,
			Target: &trafficv1alpha1.TrafficTarget{
				Kind: trafficv1alpha1.TargetKindService,
				Name: snapshot.Service,
			},
			Relay: &trafficv1alpha1.RelayEndpoint{Address: snapshot.GatewayIP},
			Ports: ports,
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
			Protocol: trafficv1alpha1.TransportProtocol(
				strings.ToUpper(string(port.Protocol)),
			),
		})
	}
	return &trafficv1alpha1.TrafficBinding{
		Namespace: snapshot.Namespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         trafficv1alpha1.TrafficBindingModePreview,
			IdentityID:   owner.IdentityID, SessionID: owner.SessionID,
			TaskID: owner.TaskID, SessionGeneration: owner.SessionGeneration,
			Relay: &trafficv1alpha1.RelayEndpoint{
				Address: snapshot.GatewayIP,
			},
			Preview: &trafficv1alpha1.PreviewExposure{
				ServiceName: snapshot.Service,
			},
			Ports: ports,
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
		Namespace: namespace,
		Spec: trafficv1alpha1.TrafficBindingSpec{
			DesiredState: trafficv1alpha1.TrafficBindingDesiredStateActive,
			Mode:         trafficv1alpha1.TrafficBindingModePortForward,
			IdentityID:   owner.IdentityID, SessionID: owner.SessionID,
			TaskID: owner.TaskID, SessionGeneration: owner.SessionGeneration,
			Target: &trafficv1alpha1.TrafficTarget{
				Kind: targetKind,
				Name: name,
			},
			Ports: []trafficv1alpha1.TrafficPort{{
				TargetPort: port,
				Protocol: trafficv1alpha1.TransportProtocol(
					strings.ToUpper(protocol),
				),
			}},
		},
	}
}
