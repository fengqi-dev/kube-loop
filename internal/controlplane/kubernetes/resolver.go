package kubernetes

import (
	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func subjectFor(principal controlplaneapi.Principal) authorization.Subject {
	return authorization.Subject{
		ID: principal.Subject, Provider: principal.Provider, Groups: append([]string(nil), principal.Groups...),
	}
}
