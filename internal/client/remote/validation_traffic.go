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

func validateLocalTargets(targets []LocalTarget) error {
	if len(targets) > 64 {
		return errors.New("local targets are invalid")
	}
	seen := make(map[string]struct{}, len(targets))
	for index := range targets {
		target := &targets[index]
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		if target.Protocol != remoteProtocolTCP && target.Protocol != remoteProtocolUDP {
			return errors.New("local target Protocol is invalid")
		}
		if target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort < 1 {
			return errors.New("local target port is invalid")
		}
		host := strings.TrimSpace(target.LocalHost)
		if host == "" {
			host = remoteLoopbackHost
		}
		if !validLocalHost(host) {
			return errors.New("local target host is invalid")
		}
		target.LocalHost = host
		key := strconv.Itoa(int(target.ServicePort)) + "/" + target.Protocol
		if _, exists := seen[key]; exists {
			return errors.New("local targets must be unique")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func validLocalHost(host string) bool {
	if address := net.ParseIP(host); address != nil {
		return !address.IsUnspecified() && !address.IsMulticast()
	}
	return validDNSSubdomain(host)
}

// validateTargetsAgainstPorts ensures every service port has exactly one matching
// local target (same protocol + service port) and that there are no extras.
// Empty targets are tolerated for backward compatibility with records that were
// created before local target persistence existed.
func validateTargetsAgainstPorts(ports []servicePortValue, targets []LocalTarget) error {
	if len(targets) == 0 {
		return nil
	}
	if len(ports) != len(targets) {
		return errors.New("local targets must match service ports")
	}
	if err := validateLocalTargets(targets); err != nil {
		return err
	}
	for _, port := range ports {
		key := strconv.Itoa(int(port.servicePort)) + "/" + port.protocol
		found := false
		for _, target := range targets {
			targetKey := strconv.Itoa(int(target.ServicePort)) + "/" + strings.ToLower(target.Protocol)
			if targetKey == key {
				found = true
				break
			}
		}
		if !found {
			return errors.New("local targets must match service ports")
		}
	}
	return nil
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
	if err != nil {
		return err
	}
	return validateTargetsAgainstPorts(ports, spec.LocalTargets)
}

//nolint:dupl // Exchange and Mirror keep distinct wire types so their APIs cannot be mixed accidentally.
func validateExchangeTask(task ExchangeTask, session Session) (ExchangeTask, error) {
	invalidIdentity := taskIdentityInvalid(task.ID, task.SessionID, task.Namespace, session)
	invalidState := !task.State.Valid() || net.ParseIP(task.ClusterIP) == nil
	invalidTimestamps := task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero()
	if invalidIdentity || invalidState || invalidTimestamps {
		return ExchangeTask{}, errors.New("gateway returned an incomplete Exchange Task")
	}
	spec := ExchangeSpec{
		Service:      task.Service,
		Ports:        append([]ExchangePort(nil), task.Ports...),
		LocalTargets: cloneLocalTargets(task.LocalTargets),
	}
	if err := validateExchangeSpec(&spec); err != nil {
		return ExchangeTask{}, errors.New("gateway returned an invalid Exchange Task")
	}
	task.Service, task.Ports, task.LocalTargets = spec.Service, spec.Ports, spec.LocalTargets
	return task, nil
}

func cloneLocalTargets(targets []LocalTarget) []LocalTarget {
	if len(targets) == 0 {
		return nil
	}
	return append([]LocalTarget(nil), targets...)
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
	if err != nil {
		return err
	}
	return validateTargetsAgainstPorts(ports, spec.LocalTargets)
}

//nolint:dupl // Exchange and Mirror keep distinct wire types so their APIs cannot be mixed accidentally.
func validateMirrorTask(task MirrorTask, session Session) (MirrorTask, error) {
	invalidIdentity := taskIdentityInvalid(task.ID, task.SessionID, task.Namespace, session)
	invalidState := !task.State.Valid() || net.ParseIP(task.ClusterIP) == nil
	invalidTimestamps := task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() || task.ExpiresAt.IsZero()
	if invalidIdentity || invalidState || invalidTimestamps {
		return MirrorTask{}, errors.New("gateway returned an incomplete Mirror Task")
	}
	spec := MirrorSpec{
		Service:      task.Service,
		Ports:        append([]MirrorPort(nil), task.Ports...),
		LocalTargets: cloneLocalTargets(task.LocalTargets),
	}
	if err := validateMirrorSpec(&spec); err != nil {
		return MirrorTask{}, errors.New("gateway returned an invalid Mirror Task")
	}
	task.Service, task.Ports, task.LocalTargets = spec.Service, spec.Ports, spec.LocalTargets
	return task, nil
}

func validatePreviewSpec(spec *PreviewSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	if !validDNSLabel(spec.Name) || len(spec.Ports) == 0 || len(spec.Ports) > 64 {
		return errors.New("preview Service name and ports are invalid")
	}
	seenPorts := make(map[string]struct{}, len(spec.Ports))
	seenNames := make(map[string]struct{}, len(spec.Ports))
	ports := make([]servicePortValue, len(spec.Ports))
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
		ports[index] = servicePortValue{name: port.Name, servicePort: port.ServicePort, protocol: port.Protocol}
	}
	return validateTargetsAgainstPorts(ports, spec.LocalTargets)
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
	spec := PreviewSpec{
		Name:         task.Name,
		Ports:        append([]PreviewPort(nil), task.Ports...),
		LocalTargets: cloneLocalTargets(task.LocalTargets),
	}
	if err := validatePreviewSpec(&spec); err != nil {
		return PreviewTask{}, errors.New("gateway returned an invalid Preview Task")
	}
	task.Name, task.Ports, task.LocalTargets = spec.Name, spec.Ports, spec.LocalTargets
	return task, nil
}
