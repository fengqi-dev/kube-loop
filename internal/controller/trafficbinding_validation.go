package controller

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type permanentError struct{ err error }

func (err permanentError) Error() string { return err.err.Error() }
func (err permanentError) Unwrap() error { return err.err }

func permanentf(format string, args ...any) error {
	return permanentError{err: fmt.Errorf(format, args...)}
}

func isPermanent(err error) bool {
	var target permanentError
	return errors.As(err, &target)
}

func validateBinding(binding *trafficv1alpha1.TrafficBinding) error {
	if binding == nil {
		return permanentf("TrafficBinding is required")
	}
	if err := validateBindingIdentity(binding); err != nil {
		return err
	}
	if err := validateBindingPorts(binding); err != nil {
		return err
	}
	if err := validateBindingMode(binding); err != nil {
		return err
	}
	if err := validateBindingTarget(binding); err != nil {
		return err
	}
	return validateBindingRelay(binding)
}

func validateBindingIdentity(binding *trafficv1alpha1.TrafficBinding) error {
	if !uuidPattern.MatchString(strings.ToLower(binding.Spec.SessionID)) {
		return permanentf("spec.sessionID must be a UUID")
	}
	if !uuidPattern.MatchString(strings.ToLower(binding.Spec.TaskID)) {
		return permanentf("spec.taskID must be a UUID")
	}
	if binding.Spec.SessionGeneration < 1 {
		return permanentf("spec.sessionGeneration must be positive")
	}
	if len(binding.Spec.Ports) < 1 || len(binding.Spec.Ports) > 64 {
		return permanentf("spec.ports must contain one to 64 mappings")
	}
	return nil
}

func validateBindingPorts(binding *trafficv1alpha1.TrafficBinding) error {
	seenPorts := make(map[string]struct{}, len(binding.Spec.Ports))
	seenNames := make(map[string]struct{}, len(binding.Spec.Ports))
	for index := range binding.Spec.Ports {
		port := &binding.Spec.Ports[index]
		if err := validatePort(port.TargetPort, fmt.Sprintf("spec.ports[%d].targetPort", index)); err != nil {
			return err
		}
		if err := validateProtocol(port.Protocol); err != nil {
			return err
		}
		if port.Protocol == "" {
			return permanentf("spec.ports[%d].protocol is required", index)
		}
		if port.RelayPort != nil {
			if err := validatePort(*port.RelayPort, fmt.Sprintf("spec.ports[%d].relayPort", index)); err != nil {
				return err
			}
		}
		if port.Name != "" {
			if problems := validation.IsDNS1123Label(port.Name); len(problems) > 0 {
				return permanentf("spec.ports[%d].name is invalid: %s", index, problems[0])
			}
			if _, exists := seenNames[port.Name]; exists {
				return permanentf("spec.ports contains duplicate name %q", port.Name)
			}
			seenNames[port.Name] = struct{}{}
		}
		key := fmt.Sprintf("%s/%d", port.Protocol, port.TargetPort)
		if _, exists := seenPorts[key]; exists {
			return permanentf("spec.ports contains duplicate target %s", key)
		}
		seenPorts[key] = struct{}{}
	}
	return nil
}

func validateBindingMode(binding *trafficv1alpha1.TrafficBinding) error {
	switch binding.Spec.Mode {
	case trafficv1alpha1.TrafficBindingModePortForward:
		if binding.Spec.Target == nil || binding.Spec.Relay != nil ||
			binding.Spec.Preview != nil || len(binding.Spec.Ports) != 1 {
			return permanentf("PortForward requires spec.target and exactly one port")
		}
		if binding.Spec.Ports[0].RelayPort != nil {
			return permanentf("PortForward forbids spec.ports[0].relayPort")
		}
	case trafficv1alpha1.TrafficBindingModePreview:
		if binding.Spec.Target != nil || binding.Spec.Relay == nil || binding.Spec.Preview == nil {
			return permanentf("Preview requires spec.relay and spec.preview")
		}
		if problems := validation.IsDNS1123Label(binding.Spec.Preview.ServiceName); len(problems) > 0 {
			return permanentf("spec.preview.serviceName is invalid: %s", problems[0])
		}
	case trafficv1alpha1.TrafficBindingModeExchange, trafficv1alpha1.TrafficBindingModeMirror:
		if binding.Spec.Target == nil || binding.Spec.Target.Kind != trafficv1alpha1.TargetKindService ||
			binding.Spec.Relay == nil || binding.Spec.Preview != nil {
			return permanentf("%s requires a Service target and spec.relay", binding.Spec.Mode)
		}
	default:
		return permanentf("spec.mode %q is unsupported", binding.Spec.Mode)
	}
	if binding.Spec.Mode != trafficv1alpha1.TrafficBindingModePortForward {
		for index := range binding.Spec.Ports {
			if binding.Spec.Ports[index].RelayPort == nil {
				return permanentf("spec.ports[%d].relayPort is required for %s", index, binding.Spec.Mode)
			}
		}
	}
	return nil
}

func validateBindingTarget(binding *trafficv1alpha1.TrafficBinding) error {
	if binding.Spec.Target != nil {
		if binding.Spec.Target.Kind != trafficv1alpha1.TargetKindPod &&
			binding.Spec.Target.Kind != trafficv1alpha1.TargetKindService {
			return permanentf("spec.target.kind %q is unsupported", binding.Spec.Target.Kind)
		}
		if problems := validation.IsDNS1123Subdomain(binding.Spec.Target.Name); len(problems) > 0 {
			return permanentf("spec.target.name is invalid: %s", problems[0])
		}
	}
	return nil
}

func validateBindingRelay(binding *trafficv1alpha1.TrafficBinding) error {
	if binding.Spec.Relay != nil {
		if net.ParseIP(strings.TrimSpace(binding.Spec.Relay.Address)) == nil {
			return permanentf("spec.relay.address must be an IP literal")
		}
	}
	return nil
}

func validatePort(port int32, field string) error {
	if port < 1 || port > 65535 {
		return permanentf("%s must be between 1 and 65535", field)
	}
	return nil
}

func validateProtocol(protocol trafficv1alpha1.TransportProtocol) error {
	switch protocol {
	case "", trafficv1alpha1.TransportProtocolTCP, trafficv1alpha1.TransportProtocolUDP:
		return nil
	default:
		return permanentf("protocol %q is unsupported", protocol)
	}
}

func normalizedProtocol(protocol trafficv1alpha1.TransportProtocol) trafficv1alpha1.TransportProtocol {
	if protocol == "" {
		return trafficv1alpha1.TransportProtocolTCP
	}
	return protocol
}
