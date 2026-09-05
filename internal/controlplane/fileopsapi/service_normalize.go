package fileopsapi

import (
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/fileapi"
)

func (handler *Service) normalize(spec *Spec) *controlplaneapi.Error {
	spec.Pod, spec.Container = strings.TrimSpace(
		spec.Pod,
	), strings.TrimSpace(
		spec.Container,
	)
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return controlplaneapi.Invalid("pod", "Pod name is invalid")
	}
	if spec.Container != "" &&
		len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return controlplaneapi.Invalid("container", "container name is invalid")
	}
	normalized, root, err := fileapi.NormalizeContainerPath(
		spec.Path,
		handler.allowedRoots,
	)
	if err != nil {
		return controlplaneapi.Invalid("path", err.Error())
	}
	spec.Path, spec.AllowedRoot = normalized, root
	spec.Kind = strings.ToLower(strings.TrimSpace(spec.Kind))
	switch spec.Action {
	case "list":
		if spec.Destination != "" || spec.Kind != "" || spec.Recursive {
			return controlplaneapi.Invalid("path", "list accepts only pod, container and path")
		}
	case ActionCreate:
		if spec.Path == root {
			return controlplaneapi.Invalid(
				"path",
				"configured allowed roots cannot be modified",
			)
		}
		if spec.Kind != KindFile && spec.Kind != KindDirectory {
			return controlplaneapi.Invalid("kind", "kind must be file or directory")
		}
		if spec.Destination != "" || spec.Recursive {
			return controlplaneapi.Invalid(
				"destination",
				"create does not accept destination or recursive",
			)
		}
	case ActionRename:
		if spec.Path == root {
			return controlplaneapi.Invalid(
				"path",
				"configured allowed roots cannot be modified",
			)
		}
		destination, destinationRoot, destinationErr := fileapi.NormalizeContainerPath(
			spec.Destination,
			handler.allowedRoots,
		)
		if destinationErr != nil {
			return controlplaneapi.Invalid("destination", destinationErr.Error())
		}
		if destination == destinationRoot {
			return controlplaneapi.Invalid(
				"destination",
				"configured allowed roots cannot be modified",
			)
		}
		if destination == spec.Path {
			return controlplaneapi.Invalid("destination", "destination must differ from path")
		}
		spec.Destination, spec.DestinationRoot = destination, destinationRoot
		if spec.Kind != "" || spec.Recursive {
			return controlplaneapi.Invalid("kind", "rename does not accept kind or recursive")
		}
	case ActionDelete:
		if spec.Path == root {
			return controlplaneapi.Invalid(
				"path",
				"configured allowed roots cannot be modified",
			)
		}
		if spec.Destination != "" || spec.Kind != "" {
			return controlplaneapi.Invalid(
				"destination",
				"delete does not accept destination or kind",
			)
		}
	default:
		return controlplaneapi.Invalid("action", "remote file action is invalid")
	}
	return nil
}
