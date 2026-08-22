package remote

import (
	"errors"
	"net"
	"path"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
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
	spec := PortForwardSpec{Kind: task.Kind, Name: task.Name, Protocol: task.Protocol, RemotePort: task.RemotePort}
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
	task.Kind, task.Name, task.Protocol = spec.Kind, spec.Name, spec.Protocol
	return task, nil
}

func taskIdentityInvalid(id, sessionID, namespace string, session Session) bool {
	_, err := uuid.Parse(id)
	return err != nil || sessionID != session.ID || namespace != session.Namespace
}

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

func validateExecSpec(spec ExecSpec) error {
	if !validDNSSubdomain(strings.TrimSpace(spec.Pod)) {
		return errors.New("pod exec target name is invalid")
	}
	if spec.Container != "" && !validDNSLabel(strings.TrimSpace(spec.Container)) {
		return errors.New("pod exec container name is invalid")
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 {
		return errors.New("pod exec command must contain 1 to 64 arguments")
	}
	total := 0
	for _, argument := range spec.Command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return errors.New("pod exec command contains an invalid argument")
		}
		total += len(argument)
	}
	if total > 16<<10 {
		return errors.New("pod exec command exceeds 16 KiB")
	}
	return nil
}

func validateExecTask(task ExecTask, session Session) (ExecTask, error) {
	if _, err := uuid.Parse(
		task.ID,
	); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		!validDNSSubdomain(task.Pod) || (task.Container != "" && !validDNSLabel(task.Container)) ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() ||
		task.ExpiresAt.IsZero() {
		return ExecTask{}, errors.New("gateway returned an incomplete Pod exec Task")
	}
	return task, nil
}

func validateFileTransferSpec(spec *FileTransferSpec) error {
	spec.Direction = strings.ToLower(strings.TrimSpace(spec.Direction))
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	spec.Checksum = strings.ToLower(strings.TrimSpace(spec.Checksum))
	spec.ResumeID = strings.ToLower(strings.TrimSpace(spec.ResumeID))
	if spec.Direction != remoteDirectionUpload && spec.Direction != remoteDirectionDownload {
		return errors.New("file transfer direction must be upload or download")
	}
	if spec.Kind != remoteKindFile && spec.Kind != remoteKindDirectory {
		return errors.New("file transfer kind must be file or directory")
	}
	if !validDNSSubdomain(spec.Pod) || (spec.Container != "" && !validDNSLabel(spec.Container)) {
		return errors.New("file transfer Pod or container is invalid")
	}
	if err := validateRemotePath(spec.RemotePath); err != nil {
		return err
	}
	switch {
	case spec.Direction == remoteDirectionUpload:
		if spec.Size == 0 || spec.Offset > spec.Size || !validDigest(spec.Checksum) {
			return errors.New("file upload size, offset or checksum is invalid")
		}
		if spec.Kind == remoteKindDirectory && spec.Offset != 0 {
			return errors.New("directory upload cannot resume from a byte offset")
		}
		if spec.ResumeID != "" {
			if spec.Kind != remoteKindFile {
				return errors.New("only file uploads support a Resume ID")
			}
			if _, err := uuid.Parse(spec.ResumeID); err != nil {
				return errors.New("file upload Resume ID is invalid")
			}
		}
	case spec.Size != 0 || spec.Checksum != "" || spec.Overwrite:
		return errors.New("file download metadata must be determined by the Gateway")
	case spec.Kind == remoteKindDirectory && spec.Offset != 0:
		return errors.New("directory download cannot resume from a byte offset")
	case spec.ResumeID != "":
		return errors.New("file downloads do not accept a Resume ID")
	}
	return nil
}

func validateRemotePath(value string) error {
	if value == "" || len(value) > 4096 || value[0] != '/' || value == "/" || strings.Contains(value, "\\") ||
		path.Clean(value) != value {
		return errors.New("file transfer remote path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("file transfer remote path is invalid")
		}
	}
	return nil
}

func validateFileTransferTask(task FileTransferTask, session Session) (FileTransferTask, error) {
	if _, err := uuid.Parse(
		task.ID,
	); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() ||
		task.ExpiresAt.IsZero() {
		return FileTransferTask{}, errors.New("gateway returned an incomplete file transfer Task")
	}
	spec := FileTransferSpec{
		Direction:  task.Direction,
		Kind:       task.Kind,
		Pod:        task.Pod,
		Container:  task.Container,
		RemotePath: task.RemotePath,
		Size:       task.Size,
		Offset:     task.Offset,
		Checksum:   task.Checksum,
		Overwrite:  task.Overwrite,
		ResumeID:   task.ResumeID,
	}
	if err := validateFileTransferSpec(&spec); err != nil {
		return FileTransferTask{}, errors.New("gateway returned an invalid file transfer Task")
	}
	task.Direction, task.Kind, task.Pod, task.Container = spec.Direction, spec.Kind, spec.Pod, spec.Container
	task.RemotePath, task.Checksum = spec.RemotePath, spec.Checksum
	task.ResumeID = spec.ResumeID
	return task, nil
}

func validatePodFileSpec(action string, spec *PodFileSpec) error {
	spec.Pod, spec.Container = strings.TrimSpace(spec.Pod), strings.TrimSpace(spec.Container)
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	if !validDNSSubdomain(spec.Pod) || (spec.Container != "" && !validDNSLabel(spec.Container)) {
		return errors.New("remote file Pod or container is invalid")
	}
	if err := validatePodFilePath(spec.Path, action == remoteActionList); err != nil {
		return err
	}
	switch action {
	case remoteActionList:
		if spec.Destination != "" || spec.Kind != "" || spec.Recursive {
			return errors.New("remote directory list contains unsupported fields")
		}
	case remoteActionCreate:
		if spec.Kind != remoteKindFile && spec.Kind != remoteKindDirectory {
			return errors.New("remote file create kind must be file or directory")
		}
		if spec.Destination != "" || spec.Recursive {
			return errors.New("remote file create contains unsupported fields")
		}
	case "rename":
		if err := validatePodFilePath(spec.Destination, false); err != nil {
			return errors.New("remote file destination is invalid")
		}
		if spec.Destination == spec.Path || spec.Kind != "" || spec.Recursive {
			return errors.New("remote file rename contains unsupported fields")
		}
	case remoteActionDelete:
		if spec.Destination != "" || spec.Kind != "" {
			return errors.New("remote file delete contains unsupported fields")
		}
	default:
		return errors.New("remote file action is invalid")
	}
	return nil
}

func validatePodFilePath(value string, allowRoot bool) error {
	invalidForm := value == "" || len(value) > 4096 || value[0] != '/'
	if invalidForm || strings.Contains(value, "\\") || path.Clean(value) != value || (!allowRoot && value == "/") {
		return errors.New("remote file path is invalid")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("remote file path is invalid")
		}
	}
	return nil
}

func validatePodFileList(list PodFileList, session Session, requested PodFileSpec) (PodFileList, error) {
	if list.SessionID != session.ID || list.Namespace != session.Namespace || list.Pod != requested.Pod ||
		list.Path != requested.Path || !validDNSLabel(list.Container) || list.Items == nil {
		return PodFileList{}, errors.New("gateway returned an invalid remote directory listing")
	}
	for _, entry := range list.Items {
		invalidName := entry.Name == "" || entry.Name == "." || entry.Name == ".."
		invalidPath := path.Base(entry.Path) != entry.Name || path.Dir(entry.Path) != list.Path
		invalidKind := entry.Kind != remoteKindFile && entry.Kind != remoteKindDirectory
		invalidKind = invalidKind && entry.Kind != "symlink" && entry.Kind != "other"
		if invalidName || invalidPath || invalidKind || entry.Size < 0 || len(entry.Mode) != 4 ||
			entry.ModifiedAt.IsZero() {
			return PodFileList{}, errors.New("gateway returned an invalid remote directory entry")
		}
		for _, character := range entry.Mode {
			if character < '0' || character > '7' {
				return PodFileList{}, errors.New("gateway returned an invalid remote directory entry")
			}
		}
	}
	return list, nil
}

func validatePodFileTask(task PodFileTask, session Session) (PodFileTask, error) {
	if _, err := uuid.Parse(
		task.ID,
	); err != nil || task.SessionID != session.ID || task.Namespace != session.Namespace ||
		!task.State.Valid() ||
		task.CreatedAt.IsZero() || task.UpdatedAt.IsZero() ||
		task.ExpiresAt.IsZero() {
		return PodFileTask{}, errors.New("gateway returned an incomplete remote file operation Task")
	}
	spec := PodFileSpec{
		Pod: task.Pod, Container: task.Container, Path: task.Path, Destination: task.Destination,
		Kind: task.Kind, Recursive: task.Recursive,
	}
	if err := validatePodFileSpec(task.Action, &spec); err != nil {
		return PodFileTask{}, errors.New("gateway returned an invalid remote file operation Task")
	}
	if task.State == remotetask.Stopped && (!task.Result.Completed || task.Result.Error != "") {
		return PodFileTask{}, errors.New("gateway returned an invalid remote file operation result")
	}
	if task.State == "failed" && (task.Result.Completed || strings.TrimSpace(task.Result.Error) == "") {
		return PodFileTask{}, errors.New("gateway returned an invalid remote file operation result")
	}
	return task, nil
}

func validDNSLabel(value string) bool {
	return !strings.Contains(value, ".") && validDNSSubdomain(value)
}
