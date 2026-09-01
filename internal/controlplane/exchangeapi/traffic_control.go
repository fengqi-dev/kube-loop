package exchangeapi

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/entity"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/servicebinding"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

func (handler *Service) trafficSession(
	ctx context.Context,
	ticketIdentity trafficcontrol.Identity,
) (controlplaneapi.Identity, sessionapi.ActiveSession, *controlplaneapi.Error) {
	identity := controlplaneapi.Identity{
		Subject:  ticketIdentity.IdentityID,
		Groups:   append([]string(nil), ticketIdentity.Groups...),
		DeviceID: ticketIdentity.DeviceID,
	}
	session, apiError := handler.sessions.RequireActive(
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

func interceptPorts(
	expected []entity.Port,
	listeners []trafficcontrol.ListenerPort,
) ([]servicebinding.InterceptPort, error) {
	if len(expected) != len(listeners) {
		return nil, errors.New(
			"gateway listener ports do not match the Exchange Task",
		)
	}
	byKey := make(map[string]trafficcontrol.ListenerPort, len(listeners))
	for _, port := range listeners {
		byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)] = port
	}
	result := make([]servicebinding.InterceptPort, 0, len(expected))
	for _, port := range expected {
		listener, ok := byKey[strings.ToLower(port.Protocol)+fmt.Sprintf("/%d", port.ServicePort)]
		if !ok || listener.Name != port.Name {
			return nil, errors.New(
				"gateway listener ports do not match the Exchange Task",
			)
		}
		result = append(result, servicebinding.InterceptPort{
			Name: port.Name, Protocol: corev1.Protocol(strings.ToUpper(port.Protocol)),
			ServicePort: port.ServicePort, ListenPort: listener.ListenPort,
		})
	}
	return result, nil
}
