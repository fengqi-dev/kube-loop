package remote

import (
	"errors"
	"path"
	"strings"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/protocol/remotetask"
)

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
