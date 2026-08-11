package session

import (
	"errors"
	"fmt"
	"net"
	"slices"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
)

func (m *Manager) EnablePodSSH(request podssh.EnableRequest) (podssh.Info, error) {
	if m.podSSH == nil {
		return podssh.Info{}, errors.New("Pod SSH is unavailable")
	}
	state := m.State()
	if state.Phase != PhaseConnected || state.Mode != ConnectionModeTUN {
		return podssh.Info{}, errors.New("Pod SSH requires an active TUN connection")
	}
	if state.Capabilities != nil && !state.Capabilities.PodExec {
		return podssh.Info{}, errors.New("current Kubernetes account cannot create pods/exec sessions")
	}
	if request.Context == "" {
		request.Context = state.Context
	}
	if request.Context != state.Context {
		return podssh.Info{}, fmt.Errorf("context %q is not the active context", request.Context)
	}
	if request.Namespace == "" || request.Pod == "" {
		return podssh.Info{}, errors.New("namespace and pod are required")
	}
	var selected *podssh.Target
	for _, pod := range state.Pods {
		if pod.Namespace != request.Namespace || pod.Name != request.Pod {
			continue
		}
		if !pod.Ready || pod.IP == "" {
			return podssh.Info{}, fmt.Errorf("Pod %s/%s is not ready", pod.Namespace, pod.Name)
		}
		container := request.Container
		if container == "" && len(pod.Containers) > 0 {
			container = pod.Containers[0]
		}
		if container == "" {
			return podssh.Info{}, fmt.Errorf("Pod %s/%s has no regular containers", pod.Namespace, pod.Name)
		}
		found := slices.Contains(pod.Containers, container)
		if !found {
			return podssh.Info{}, fmt.Errorf(
				"container %q not found in Pod %s/%s", container, pod.Namespace, pod.Name,
			)
		}
		target := podssh.Target{
			Context: request.Context, Namespace: pod.Namespace, Pod: pod.Name,
			Container: container, IP: pod.IP,
		}
		selected = &target
		break
	}
	if selected == nil {
		return podssh.Info{}, fmt.Errorf("Pod %s/%s not found", request.Namespace, request.Pod)
	}
	info, err := m.podSSH.Enable(*selected)
	if err != nil {
		return podssh.Info{}, err
	}
	m.AppendLog("INFO", fmt.Sprintf(
		"Pod SSH enabled: %s/%s container=%s address=%s:22",
		info.Namespace, info.Pod, info.Container, info.IP,
	))
	return info, nil
}

func (m *Manager) DisablePodSSH(id string) error {
	if m.podSSH == nil {
		return errors.New("Pod SSH is unavailable")
	}
	if err := m.podSSH.Disable(id); err != nil {
		return err
	}
	m.AppendLog("INFO", "Pod SSH disabled: "+id)
	return nil
}

func (m *Manager) ListPodSSH() []podssh.Info {
	if m.podSSH == nil {
		return nil
	}
	return m.podSSH.List()
}

func (m *Manager) syncDefaultPodSSH(state State, pods []cluster.PodInfo) {
	if m.podSSH == nil {
		return
	}
	refs := make([]podssh.PodRef, 0, len(pods))
	if state.Phase == PhaseConnected &&
		state.Mode == ConnectionModeTUN &&
		(state.Capabilities == nil || state.Capabilities.PodExec) {
		for _, pod := range pods {
			if !pod.Ready || pod.IP == "" || len(pod.Containers) == 0 {
				continue
			}
			refs = append(refs, podssh.PodRef{
				Context: state.Context, Namespace: pod.Namespace, Pod: pod.Name,
				IP: pod.IP, Containers: append([]string{}, pod.Containers...),
			})
		}
	}
	if err := m.podSSH.Reconcile(refs); err != nil {
		m.recordLog("ERROR", "could not enable default Pod SSH endpoints: "+err.Error())
	}
}

func (m *Manager) podSSHHostTCP(host string, port uint16) (func(net.Conn), bool) {
	if m.podSSH == nil {
		return nil, false
	}
	return m.podSSH.HostTCP(host, port)
}
