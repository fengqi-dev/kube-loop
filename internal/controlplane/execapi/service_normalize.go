package execapi

import (
	"errors"
	"fmt"
	"math"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/execstream"
)

func statusFromError(err error, cancelled bool) execstream.ExitStatus {
	status := execstream.ExitStatus{Cancelled: cancelled}
	if err == nil {
		return status
	}
	status.Code = 1
	var exitError interface{ ExitStatus() int }
	if errors.As(err, &exitError) {
		exitStatus := exitError.ExitStatus()
		if exitStatus >= 0 && uint64(exitStatus) <= math.MaxUint32 {
			status.Code = uint32(exitStatus)
		}
	}
	if !cancelled {
		status.Error = "command exited unsuccessfully"
	}
	return status
}

func normalizeSpec(spec *Spec) *controlplaneapi.Error {
	spec.Pod = strings.TrimSpace(spec.Pod)
	spec.Container = strings.TrimSpace(spec.Container)
	if len(validation.IsDNS1123Subdomain(spec.Pod)) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "pod",
			Message: "Pod name is invalid",
		}
	}
	if spec.Container != "" && len(validation.IsDNS1123Label(spec.Container)) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "container",
			Message: "container name is invalid",
		}
	}
	if len(spec.Command) == 0 || len(spec.Command) > 64 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "command",
			Message: "command must contain 1 to 64 arguments",
		}
	}
	total := 0
	for index, argument := range spec.Command {
		if argument == "" || len(argument) > 4096 || strings.IndexByte(argument, 0) >= 0 {
			return &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   fmt.Sprintf("command[%d]", index),
				Message: "command argument is invalid",
			}
		}
		total += len(argument)
	}
	if total > 16<<10 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   "command",
			Message: "command exceeds 16 KiB",
		}
	}
	return nil
}
