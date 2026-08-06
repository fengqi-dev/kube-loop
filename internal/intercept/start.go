package intercept

import (
	"context"
	"fmt"
	"maps"
)

func (m *Manager) StartIntercept(ctx context.Context, mapping Mapping) (Info, error) {
	mapping.Mode = ModeExchange
	return m.startServiceIntercept(ctx, mapping)
}

func (m *Manager) StartMirror(ctx context.Context, mapping Mapping) (Info, error) {
	mapping.Mode = ModeMirror
	return m.startServiceIntercept(ctx, mapping)
}

func (m *Manager) startServiceIntercept(ctx context.Context, mapping Mapping) (Info, error) {
	if mapping.Namespace == "" {
		mapping.Namespace = "default"
	}
	if mapping.Service == "" {
		return Info{}, fmt.Errorf("service is required")
	}
	mode := mapping.Mode
	if mode == "" {
		mode = ModeExchange
	}
	if mode != ModeExchange && mode != ModeMirror {
		return Info{}, fmt.Errorf("unsupported intercept mode %q", mode)
	}

	m.mu.Lock()
	control, controlGeneration, controlReady := m.control.snapshot()
	if !m.active || !controlReady {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("session is not connected")
	}
	key := mapping.Namespace + "/" + mapping.Service
	if existing := m.registry.getByKey(key); existing != nil {
		m.mu.Unlock()
		return Info{}, conflictError(key, mode, existing)
	}
	reservation, reserved := m.registry.reserve(key)
	if !reserved {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("service %s start is already in progress", key)
	}
	contextName := m.contextName
	gatewayIP := m.gatewayIP
	m.mu.Unlock()
	defer m.releaseStartReservation(key, reservation)

	service, err := m.cluster.GetService(ctx, contextName, mapping.Namespace, mapping.Service)
	if err != nil {
		return Info{}, err
	}
	locals, err := mapping.resolveLocals(service)
	if err != nil {
		return Info{}, err
	}
	ports, err := buildPortsForLocals(service, locals, m.allocateListenPort)
	if err != nil {
		return Info{}, err
	}

	interceptID := fmt.Sprintf("%s/%s", mapping.Namespace, mapping.Service)
	transaction := newStartTransaction(control)
	defer transaction.rollback()
	if err := transaction.registerPorts(interceptID, ports, locals); err != nil {
		return Info{}, err
	}
	portKeys := transaction.portKeys

	selector := map[string]string{}
	maps.Copy(selector, service.Selector)
	lease, backends, err := m.cluster.ApplyServiceIntercept(ctx, contextName, ServiceInterceptRequest{
		Namespace: mapping.Namespace,
		Service:   mapping.Service,
		Selector:  selector,
		Ports:     ports,
		GatewayIP: gatewayIP,
		ID:        interceptID,
	})
	if err != nil {
		return Info{}, err
	}
	transaction.compensate(func() {
		_ = lease.Release(ctx)
	})

	var primaryAddrs map[string]string
	if mode == ModeMirror {
		primaryAddrs, err = buildPrimaryAddrs(backends, ports, portKeys, interceptID)
		if err != nil {
			return Info{}, err
		}
	}

	info := Info{
		ID:        interceptID,
		Namespace: mapping.Namespace,
		Service:   mapping.Service,
		ClusterIP: service.ClusterIP,
		Mode:      mode,
		Ports:     ports,
		Locals:    locals,
	}
	m.mu.Lock()
	if !m.active ||
		!m.control.matches(control, controlGeneration) ||
		!m.registry.reserved(key, reservation) {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("session changed while starting service %s", key)
	}
	hostKeys := m.routes.install(service, ports, portKeys, primaryAddrs, mode, false, interceptID)
	m.registry.add(&runtimeIntercept{
		info: info, lease: lease, portKeys: portKeys, primaryAddrs: primaryAddrs, hostKeys: hostKeys,
	})
	m.registry.release(key, reservation)
	m.mu.Unlock()
	transaction.commit()
	return info, nil
}

func (m *Manager) StartPreview(ctx context.Context, request PreviewRequest) (Info, error) {
	if request.Namespace == "" {
		request.Namespace = "default"
	}
	if request.Name == "" {
		return Info{}, fmt.Errorf("service name is required")
	}
	locals, err := normalizePreviewPorts(request.Ports)
	if err != nil {
		return Info{}, err
	}

	m.mu.Lock()
	control, controlGeneration, controlReady := m.control.snapshot()
	if !m.active || !controlReady {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("session is not connected")
	}
	key := request.Namespace + "/" + request.Name
	if m.registry.containsKey(key) {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("service %s is already in use", key)
	}
	reservation, reserved := m.registry.reserve(key)
	if !reserved {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("service %s start is already in progress", key)
	}
	contextName := m.contextName
	gatewayIP := m.gatewayIP
	m.mu.Unlock()
	defer m.releaseStartReservation(key, reservation)

	ports := buildPreviewPorts(locals, m.allocateListenPort)

	previewID := fmt.Sprintf("%s/%s", request.Namespace, request.Name)
	transaction := newStartTransaction(control)
	defer transaction.rollback()
	if err := transaction.registerPorts(previewID, ports, locals); err != nil {
		return Info{}, err
	}
	portKeys := transaction.portKeys

	service, lease, err := m.cluster.CreatePreviewService(ctx, contextName, PreviewServiceRequest{
		Namespace: request.Namespace,
		Service:   request.Name,
		Ports:     ports,
		GatewayIP: gatewayIP,
		ID:        previewID,
	})
	if err != nil {
		return Info{}, err
	}
	transaction.compensate(func() {
		_ = lease.Release(ctx)
	})

	info := Info{
		ID:        previewID,
		Namespace: request.Namespace,
		Service:   request.Name,
		ClusterIP: service.ClusterIP,
		Preview:   true,
		Ports:     ports,
		Locals:    locals,
	}
	m.mu.Lock()
	if !m.active ||
		!m.control.matches(control, controlGeneration) ||
		!m.registry.reserved(key, reservation) {
		m.mu.Unlock()
		return Info{}, fmt.Errorf("session changed while starting service %s", key)
	}
	hostKeys := m.routes.install(service, ports, portKeys, nil, ModeExchange, true, previewID)
	m.registry.add(&runtimeIntercept{
		info: info, lease: lease, portKeys: portKeys, hostKeys: hostKeys,
	})
	m.registry.release(key, reservation)
	m.mu.Unlock()
	transaction.commit()
	return info, nil
}
