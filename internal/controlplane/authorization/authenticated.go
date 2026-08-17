package authorization

import (
	"context"
	"strings"
)

type Subject struct {
	ID       string
	Provider string
	Groups   []string
}

type Request struct {
	Operation       string
	Namespace       string
	ResourceKind    string
	ResourceName    string
	NamespaceLabels map[string]string
	LabelsAvailable bool
}

type Decision struct {
	Allowed bool
}

type Authorizer interface {
	Authorize(context.Context, Subject, Request) Decision
}

// Authenticated allows every request made by an authenticated identity.
// Authentication remains the only application access-control boundary.
type Authenticated struct{}

func NewAuthenticated() Authenticated { return Authenticated{} }

func (Authenticated) Authorize(_ context.Context, subject Subject, _ Request) Decision {
	return Decision{Allowed: strings.TrimSpace(subject.ID) != ""}
}
