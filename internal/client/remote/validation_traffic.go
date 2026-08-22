package remote

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

type servicePortValue struct {
	name        string
	servicePort int32
	protocol    string
}

func validateServiceSpec(service *string, ports []servicePortValue, subject string) error {
	*service = strings.TrimSpace(*service)
	if !validDNSSubdomain(*service) || len(ports) == 0 || len(ports) > 64 {
		return errors.New(subject + " Service and ports are invalid")
	}
	seen := make(map[string]struct{}, len(ports))
	for index := range ports {
		port := &ports[index]
		port.name = strings.TrimSpace(port.name)
		port.protocol = strings.ToLower(strings.TrimSpace(port.protocol))
		invalidProtocol := port.protocol != remoteProtocolTCP && port.protocol != remoteProtocolUDP
		if port.servicePort < 1 || port.servicePort > 65535 || invalidProtocol {
			return errors.New(subject + " Service port is invalid")
		}
		key := strconv.Itoa(int(port.servicePort)) + "/" + port.protocol
		if _, exists := seen[key]; exists {
			return errors.New(subject + " Service ports must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validateExchangeSpec(spec *ExchangeSpec) error {
	ports := make([]servicePortValue, len(spec.Ports))
	for index, port := range spec.Ports {
		ports[index] = servicePortValue{name: port.Name, servicePort: port.ServicePort, protocol: port.Protocol}
	}
	err := validateServiceSpec(&spec.Service, ports, "Exchange")
	for index, port := range ports {
		spec.Ports[index].Name, spec.Ports[index].Protocol = port.name, port.protocol
	}
	return err
}

func validateExchangeTask(task ExchangeTask, session Session) (ExchangeTask, error) {
	invalidIdentity := taskIdentityInvalid(task.ID, task.SessionID, task.Namespace, session)
	invalidState := !task.State.Valid() || net.ParseIP(task.ClusterIP) == nil
	invalidTimestamps := task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero()
	if invalidIdentity || invalidState || invalidTimestamps {
		return ExchangeTask{}, errors.New("gateway returned an incomplete Exchange Task")
	}
	spec := ExchangeSpec{Service: task.Service, Ports: append([]ExchangePort(nil), task.Ports...)}
	if err := validateExchangeSpec(&spec); err != nil {
		return ExchangeTask{}, errors.New("gateway returned an invalid Exchange Task")
	}
	task.Service, task.Ports = spec.Service, spec.Ports
	return task, nil
}

func validateMirrorSpec(spec *MirrorSpec) error {
	ports := make([]servicePortValue, len(spec.Ports))
	for index, port := range spec.Ports {
		ports[index] = servicePortValue{name: port.Name, servicePort: port.ServicePort, protocol: port.Protocol}
	}
	err := validateServiceSpec(&spec.Service, ports, "Mirror")
	for index, port := range ports {
		spec.Ports[index].Name, spec.Ports[index].Protocol = port.name, port.protocol
	}
	return err
}

func validateMirrorTask(task MirrorTask, session Session) (MirrorTask, error) {
	invalidIdentity := taskIdentityInvalid(task.ID, task.SessionID, task.Namespace, session)
	invalidState := !task.State.Valid() || net.ParseIP(task.ClusterIP) == nil
	invalidTimestamps := task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero()
	if invalidIdentity || invalidState || invalidTimestamps {
		return MirrorTask{}, errors.New("gateway returned an incomplete Mirror Task")
	}
	spec := MirrorSpec{Service: task.Service, Ports: append([]MirrorPort(nil), task.Ports...)}
	if err := validateMirrorSpec(&spec); err != nil {
		return MirrorTask{}, errors.New("gateway returned an invalid Mirror Task")
	}
	task.Service, task.Ports = spec.Service, spec.Ports
	return task, nil
}

func validatePreviewSpec(spec *PreviewSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if !validDNSLabel(spec.Name) || len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return errors.New("preview Service name and ports are invalid")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	for index := range spec.Ports {
		port := &spec.Ports[index]
		port.Name = strings.TrimSpace(port.Name)
		port.Protocol = strings.ToLower(strings.TrimSpace(port.Protocol))
		invalidProtocol := port.Protocol != remoteProtocolTCP && port.Protocol != remoteProtocolUDP
		invalidName := port.Name != "" && !validDNSLabel(port.Name)
		if port.ServicePort < 1 || port.ServicePort > 65535 || invalidProtocol || invalidName {
			return errors.New("preview Service port is invalid")
		}
		key := strconv.Itoa(int(port.ServicePort)) + "/" + port.Protocol
		if _, exists := seenPorts[key]; exists {
			return errors.New("preview Service ports must be unique")
		}
		if port.Name != "" {
			if _, exists := seenNames[port.Name]; exists {
				return errors.New("preview Service port names must be unique")
			}
			seenNames[port.Name] = struct{}{}
		}
		seenPorts[key] = struct{}{}
	}
	return nil
}

func validatePreviewTask(task PreviewTask, session Session) (PreviewTask, error) {
	clusterIP := net.ParseIP(task.ClusterIP)
	invalidIdentity := taskIdentityInvalid(task.ID, task.SessionID, task.Namespace, session)
	missingClusterIP := task.ClusterIP != "" && clusterIP == nil
	missingRunningClusterIP := task.State == remotetask.Running && clusterIP == nil
	invalidState := !task.State.Valid() || missingClusterIP || missingRunningClusterIP
	if invalidIdentity || invalidState || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() {
		return PreviewTask{}, errors.New("gateway returned an incomplete Preview Task")
	}
	spec := PreviewSpec{Name: task.Name, Ports: append([]PreviewPort(nil), task.Ports...)}
	if err := validatePreviewSpec(&spec); err != nil {
		return PreviewTask{}, errors.New("gateway returned an invalid Preview Task")
	}
	task.Name, task.Ports = spec.Name, spec.Ports
	return task, nil
}
