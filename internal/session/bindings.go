package session

import (
	"context"
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/intercept"
	"github.com/fengqi-dev/kube-loop/internal/portfwd"
)

func (m *Manager) StartIntercept(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error) {
	m.AppendLog("INFO", fmt.Sprintf("starting exchange %s/%s", mapping.Namespace, mapping.Service))
	info, err := m.intercept.StartIntercept(ctx, mapping)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"start exchange %s/%s: %v", mapping.Namespace, mapping.Service, err,
		))
	} else if !m.isRestoring() {
		m.persistExchanges(m.State().Context)
		m.AppendLog("INFO", fmt.Sprintf("started exchange %s/%s", mapping.Namespace, mapping.Service))
	}
	return info, err
}

func (m *Manager) StartMirror(ctx context.Context, mapping intercept.Mapping) (intercept.Info, error) {
	m.AppendLog("INFO", fmt.Sprintf("starting mirror %s/%s", mapping.Namespace, mapping.Service))
	info, err := m.intercept.StartMirror(ctx, mapping)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"start mirror %s/%s: %v", mapping.Namespace, mapping.Service, err,
		))
	} else if !m.isRestoring() {
		m.persistMirrors(m.State().Context)
		m.AppendLog("INFO", fmt.Sprintf("started mirror %s/%s", mapping.Namespace, mapping.Service))
	}
	return info, err
}

func (m *Manager) StopIntercept(ctx context.Context, id string) error {
	m.AppendLog("INFO", "stopping intercept "+id)
	err := m.intercept.Stop(ctx, id)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("stop intercept %s: %v", id, err))
	} else if !m.isRestoring() {
		m.persistExchanges(m.State().Context)
		m.persistMirrors(m.State().Context)
		m.AppendLog("INFO", "stopped intercept "+id)
	}
	return err
}

func (m *Manager) ListIntercepts() []intercept.Info {
	return m.intercept.List()
}

func (m *Manager) ListMirrors() []intercept.Info {
	return m.intercept.ListMirrors()
}

func (m *Manager) StartPreview(ctx context.Context, request intercept.PreviewRequest) (intercept.Info, error) {
	m.AppendLog("INFO", fmt.Sprintf("starting preview %s/%s", request.Namespace, request.Name))
	info, err := m.intercept.StartPreview(ctx, request)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"start preview %s/%s: %v", request.Namespace, request.Name, err,
		))
	} else if !m.isRestoring() {
		m.persistPreviews(m.State().Context)
		m.AppendLog("INFO", fmt.Sprintf("started preview %s/%s", request.Namespace, request.Name))
	}
	return info, err
}

func (m *Manager) StopPreview(ctx context.Context, id string) error {
	m.AppendLog("INFO", "stopping preview "+id)
	err := m.intercept.Stop(ctx, id)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("stop preview %s: %v", id, err))
	} else if !m.isRestoring() {
		m.persistPreviews(m.State().Context)
		m.AppendLog("INFO", "stopped preview "+id)
	}
	return err
}

func (m *Manager) StartPortForwardSession(
	ctx context.Context, request portfwd.Request,
) (portfwd.Info, error) {
	m.AppendLog("INFO", fmt.Sprintf(
		"starting port-forward %s/%s/%s:%d/%s on local port %d",
		request.Kind, request.Namespace, request.Name, request.RemotePort,
		request.Protocol, request.LocalPort,
	))
	info, err := m.portfwd.Start(ctx, request)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf(
			"start port-forward %s/%s/%s:%d/%s: %v",
			request.Kind, request.Namespace, request.Name, request.RemotePort,
			request.Protocol, err,
		))
	} else {
		m.persistPortForwards()
		m.AppendLog("INFO", fmt.Sprintf(
			"started port-forward %s/%s/%s:%d/%s → :%d",
			request.Kind, request.Namespace, request.Name, request.RemotePort,
			info.Protocol, info.LocalPort,
		))
	}
	return info, err
}

func (m *Manager) StopPortForward(id string) error {
	m.AppendLog("INFO", "stopping port-forward "+id)
	err := m.portfwd.Stop(id)
	if err != nil {
		m.AppendLog("ERROR", fmt.Sprintf("stop port-forward %s: %v", id, err))
	} else {
		m.persistPortForwards()
		m.AppendLog("INFO", "stopped port-forward "+id)
	}
	return err
}

func (m *Manager) ListPortForwards() []portfwd.Info {
	return m.portfwd.List()
}

func (m *Manager) StopAllPortForwards() {
	m.portfwd.StopAll()
}
