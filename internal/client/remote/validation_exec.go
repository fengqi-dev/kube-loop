package remote

import (
	"errors"
	"strings"

	"github.com/google/uuid"
)

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
