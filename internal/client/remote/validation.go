package remote

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
)

func validateNamespace(namespace string) error {
	if namespace == "" || len(namespace) > 63 || namespace[0] == '-' || namespace[len(namespace)-1] == '-' {
		return errors.New("namespace is invalid")
	}
	for _, character := range namespace {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
			continue
		}
		return errors.New("namespace is invalid")
	}
	return nil
}

func validateSessionTarget(namespace, sessionID string) error {
	if err := validateNamespace(namespace); err != nil {
		return err
	}
	if _, err := uuid.Parse(strings.TrimSpace(sessionID)); err != nil {
		return errors.New("session ID is invalid")
	}
	return nil
}

func validateSession(session Session, namespace string) (Session, error) {
	if err := validateSessionTarget(namespace, session.ID); err != nil || session.Namespace != namespace ||
		session.Generation == 0 || session.State == "" || session.CreatedAt.IsZero() || session.ExpiresAt.IsZero() ||
		!validDigest(session.NetworkSpecHash) {
		return Session{}, errors.New("gateway returned an incomplete Session")
	}
	normalized, err := networkspec.Normalize(session.NetworkSpec)
	if err != nil {
		return Session{}, errors.New("gateway returned an invalid NetworkSpec")
	}
	hash, err := networkspec.Hash(normalized)
	if err != nil || hash != session.NetworkSpecHash {
		return Session{}, errors.New("gateway returned a mismatched NetworkSpec hash")
	}
	session.NetworkSpec = normalized
	return session, nil
}

func validDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validatePortForwardSpec(spec *PortForwardSpec) error {
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Protocol = strings.ToLower(strings.TrimSpace(spec.Protocol))
	if spec.Kind != "pod" && spec.Kind != remoteResourceService {
		return errors.New("port Forward kind must be pod or service")
	}
	if !validDNSSubdomain(spec.Name) {
		return errors.New("port Forward target name is invalid")
	}
	if spec.Protocol == "" {
		spec.Protocol = remoteProtocolTCP
	}
	if spec.Protocol != remoteProtocolTCP && spec.Protocol != remoteProtocolUDP {
		return errors.New("port Forward protocol must be tcp or udp")
	}
	if spec.RemotePort == 0 {
		return errors.New("port Forward remote port is required")
	}
	return nil
}

func validDNSSubdomain(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for label := range strings.SplitSeq(value, ".") {
		if label == "" || len(label) > 63 || !isLowerAlphaNumeric(label[0]) ||
			!isLowerAlphaNumeric(label[len(label)-1]) {
			return false
		}
		for _, character := range label {
			if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-' {
				continue
			}
			return false
		}
	}
	return true
}

func isLowerAlphaNumeric(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9')
}

func validatePortForwardTask(task PortForwardTask, session Session) (PortForwardTask, error) {
	if _, err := uuid.Parse(
		task.ID,
	); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() || task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() ||
		task.ExpiresAt.IsZero() {
		return PortForwardTask{}, errors.New("gateway returned an incomplete Port Forward Task")
	}
	spec := PortForwardSpec{
		Kind: task.Kind, Name: task.Name, Protocol: task.Protocol,
		RemotePort: task.RemotePort, LocalPort: task.LocalPort,
	}
	if err := validatePortForwardSpec(&spec); err != nil {
		return PortForwardTask{}, errors.New("gateway returned an invalid Port Forward Task")
	}
	host, rawPort, err := net.SplitHostPort(task.DialAddress)
	if err != nil || net.ParseIP(host) == nil {
		return PortForwardTask{}, errors.New("gateway returned an invalid Port Forward target")
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil || port == 0 {
		return PortForwardTask{}, errors.New("gateway returned an invalid Port Forward target")
	}
	task.Kind, task.Name, task.Protocol, task.LocalPort = spec.Kind, spec.Name, spec.Protocol, spec.LocalPort
	return task, nil
}

func taskIdentityInvalid(id, sessionID, namespace string, session Session) bool {
	_, err := uuid.Parse(id)
	return err != nil || sessionID != session.ID || namespace != session.Namespace
}

func validDNSLabel(value string) bool {
	return !strings.Contains(value, ".") && validDNSSubdomain(value)
}
