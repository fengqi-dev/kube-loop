// Package trafficapi holds what the traffic task APIs -- exchangeapi,
// mirrorapi and previewapi -- do identically. Each of those packages keeps its
// own wire types, routes and spec rules, because their HTTP contracts are
// separate and must stay unmixable; what lives here is the machinery none of
// them varies: local target normalization, the shared error shapes, and the
// traffic-control handshake.
package trafficapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"

	"github.com/labstack/echo/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/storage"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficbindingclient"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// Errors builds the API errors for one traffic task type. Name is the
// user-facing noun -- "Exchange", "Mirror", "Preview" -- that appears in the
// messages, so a client can tell which API rejected the request.
type Errors struct {
	Name string
}

// Target maps a Kubernetes error from resolving the target Service.
func (reporter Errors) Target(err error) *controlplaneapi.Error {
	switch {
	case apierrors.IsForbidden(err):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeForbidden,
			Message: "Kubernetes " + reporter.Name + " access is not permitted",
			Cause:   err,
		}
	case apierrors.IsNotFound(err):
		return NotFound()
	default:
		return Invalid("service", err.Error())
	}
}

// Storage maps a persistence error, collapsing every conflict shape onto one
// client-visible conflict.
func (reporter Errors) Storage(err error) *controlplaneapi.Error {
	switch {
	case errors.Is(err, storage.ErrNotFound):
		return NotFound()
	case errors.Is(err, storage.ErrConflict),
		errors.Is(err, storage.ErrIdempotencyMismatch),
		errors.Is(err, trafficbindingclient.ErrTrafficBindingConflict):
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: reporter.Name + " Task conflicts with existing state",
			Cause:   err,
		}
	default:
		return reporter.Internal(err)
	}
}

// Internal reports a failure the client cannot act on.
func (reporter Errors) Internal(err error) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInternal,
		Message: reporter.Name + " operation failed",
		Cause:   err,
	}
}

// Invalid rejects one named request field.
func Invalid(field, message string) *controlplaneapi.Error {
	return controlplaneapi.Invalid(field, message)
}

// NotFound reports a resource the caller may not learn anything about.
func NotFound() *controlplaneapi.Error { return controlplaneapi.NotFound() }

func WriteJSON(ctx *echo.Context, status int, value any) {
	_ = ctx.JSON(status, value)
}

// NormalizeLocalTargets trims, defaults and sorts the local targets in place,
// rejecting any that does not pair with exactly one of the Service ports.
func NormalizeLocalTargets(
	targets *[]entity.LocalTarget,
	ports []entity.Port,
) *controlplaneapi.Error {
	if len(*targets) == 0 {
		return nil
	}
	if len(*targets) != len(ports) {
		return Invalid("localTargets", "local targets must match Service ports")
	}
	expected := make(map[string]struct{}, len(ports))
	for _, port := range ports {
		expected[localTargetKey(port.ServicePort, port.Protocol)] = struct{}{}
	}
	seen := make(map[string]struct{}, len(*targets))
	for index := range *targets {
		target := &(*targets)[index]
		target.Protocol = strings.ToLower(strings.TrimSpace(target.Protocol))
		target.LocalHost = strings.TrimSpace(target.LocalHost)
		if target.LocalHost == "" {
			target.LocalHost = "127.0.0.1"
		}
		address := net.ParseIP(target.LocalHost)
		invalidHost := address != nil && (address.IsUnspecified() || address.IsMulticast())
		invalidHost = invalidHost || address == nil && len(validation.IsDNS1123Subdomain(target.LocalHost)) != 0
		key := localTargetKey(target.ServicePort, target.Protocol)
		_, matchesPort := expected[key]
		_, duplicate := seen[key]
		if target.ServicePort < 1 || target.ServicePort > 65535 || target.LocalPort < 1 ||
			(target.Protocol != "tcp" && target.Protocol != "udp") ||
			invalidHost || !matchesPort || duplicate {
			return Invalid("localTargets", "local target is invalid")
		}
		seen[key] = struct{}{}
	}
	slices.SortFunc(*targets, CompareLocalTargets)
	return nil
}

func localTargetKey(port int32, protocol string) string {
	return fmt.Sprintf("%d/%s", port, strings.ToLower(strings.TrimSpace(protocol)))
}

func CompareLocalTargets(left, right entity.LocalTarget) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

func ComparePorts(left, right entity.Port) int {
	if left.ServicePort != right.ServicePort {
		return int(left.ServicePort - right.ServicePort)
	}
	return strings.Compare(left.Protocol, right.Protocol)
}

// SessionValidator is the slice of the session API the traffic-control
// handshake needs. Each task API declares the same interface for its own
// constructor; Go's structural typing lets one implementation satisfy both.
type SessionValidator interface {
	RequireActive(
		context.Context,
		controlplaneapi.Identity,
		string,
		string,
	) (sessionapi.ActiveSession, *controlplaneapi.Error)
}

// TrafficSession resolves the identity carried by a relay ticket into an
// active session, rejecting a ticket whose generation the session has moved past.
func TrafficSession(
	ctx context.Context,
	sessions SessionValidator,
	ticketIdentity trafficcontrol.Identity,
) (controlplaneapi.Identity, sessionapi.ActiveSession, *controlplaneapi.Error) {
	identity := controlplaneapi.Identity{
		Subject:  ticketIdentity.IdentityID,
		Groups:   append([]string(nil), ticketIdentity.Groups...),
		DeviceID: ticketIdentity.DeviceID,
	}
	session, apiError := sessions.RequireActive(
		ctx,
		identity,
		ticketIdentity.Namespace,
		ticketIdentity.SessionID,
	)
	if apiError != nil {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, apiError
	}
	if !sessionapi.AcceptsStreamGeneration(
		session.Generation,
		ticketIdentity.SessionGeneration,
	) {
		return controlplaneapi.Identity{}, sessionapi.ActiveSession{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeConflict,
			Message: "Session generation changed",
		}
	}
	return identity, session, nil
}

// InterceptPorts pairs the task's Service ports with the Gateway's listener
// ports. Name appears in the mismatch error so the client knows which task
// the Gateway disagreed about.
func InterceptPorts(
	name string,
	expected []entity.Port,
	listeners []trafficcontrol.ListenerPort,
) ([]servicebinding.InterceptPort, error) {
	mismatch := errors.New("gateway listener ports do not match the " + name + " Task")
	if len(expected) != len(listeners) {
		return nil, mismatch
	}
	byKey := make(map[string]trafficcontrol.ListenerPort, len(listeners))
	for _, port := range listeners {
		byKey[listenerKey(port.Protocol, port.ServicePort)] = port
	}
	result := make([]servicebinding.InterceptPort, 0, len(expected))
	for _, port := range expected {
		listener, ok := byKey[listenerKey(port.Protocol, port.ServicePort)]
		if !ok || listener.Name != port.Name {
			return nil, mismatch
		}
		result = append(result, servicebinding.InterceptPort{
			Name: port.Name, Protocol: corev1.Protocol(strings.ToUpper(port.Protocol)),
			ServicePort: port.ServicePort, ListenPort: listener.ListenPort,
		})
	}
	return result, nil
}

func listenerKey(protocol string, servicePort int32) string {
	return strings.ToLower(protocol) + fmt.Sprintf("/%d", servicePort)
}
