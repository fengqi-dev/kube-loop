package sshserver

import (
	"cmp"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
)

func (s *Server) Disable(id string) error {
	s.mu.Lock()
	found := false
	for ip, target := range s.targets {
		if targetID(target) == id {
			delete(s.targets, ip)
			found = true
			break
		}
	}
	if !found {
		s.mu.Unlock()
		return fmt.Errorf("pod SSH endpoint %q not found", id)
	}
	connections := make([]net.Conn, 0)
	done := make([]<-chan struct{}, 0)
	for connection, target := range s.connections {
		if target == id {
			connections = append(connections, connection)
			done = append(done, s.connectionDone[connection])
		}
	}
	s.mu.Unlock()
	closeSSHConnections(connections, done)
	return nil
}

func (s *Server) List() []Info {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	items := make([]Info, 0, len(s.targets))
	for _, target := range s.targets {
		items = append(items, s.info(target))
	}
	s.mu.RUnlock()
	slices.SortFunc(items, func(a, b Info) int {
		if order := cmp.Compare(a.Namespace, b.Namespace); order != 0 {
			return order
		}
		return cmp.Compare(a.Pod, b.Pod)
	})
	return items
}

// Command returns the SSH command for an active endpoint and validates the
// requested container against the Pod's current container inventory.
func (s *Server) Command(id, container string) (string, error) {
	if s == nil {
		return "", errors.New("pod SSH is unavailable")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, target := range s.targets {
		if targetID(target) != id {
			continue
		}
		selected, ok := targetForLogin(target, container)
		if !ok {
			return "", fmt.Errorf(
				"container %q is not available for Pod SSH", container,
			)
		}
		return s.info(selected).Command, nil
	}
	return "", fmt.Errorf("pod SSH endpoint %q not found", id)
}

// Reconcile exposes every supplied Pod by default, follows replacement IPs,
// and drops endpoints whose Pod/container disappeared.
func (s *Server) Reconcile(pods []PodRef) error {
	if s == nil {
		return nil
	}
	live := make(map[string]PodRef, len(pods))
	for _, pod := range pods {
		ip, err := netip.ParseAddr(strings.TrimSpace(pod.IP))
		if err != nil || len(pod.Containers) == 0 {
			continue
		}
		pod.IP = ip.Unmap().String()
		live[podIdentity(pod.Context, pod.Namespace, pod.Pod)] = pod
	}
	if len(live) > 0 {
		if _, err := s.signer(); err != nil {
			return err
		}
		if _, err := s.authorizedClientKeys(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	current := make(map[string]Target, len(s.targets))
	for _, target := range s.targets {
		current[targetID(target)] = target
	}
	next := make(map[string]Target, len(live))
	activeIDs := make(map[string]struct{}, len(live))
	for id, pod := range live {
		container := pod.Containers[0]
		if target, ok := current[id]; ok && slices.Contains(pod.Containers, target.Container) {
			container = target.Container
		}
		target := Target{
			Context: pod.Context, Namespace: pod.Namespace, Pod: pod.Pod,
			Container: container, Containers: append([]string{}, pod.Containers...), IP: pod.IP,
		}
		next[id] = target
		activeIDs[id] = struct{}{}
	}
	s.targets = next
	connections := make([]net.Conn, 0)
	done := make([]<-chan struct{}, 0)
	for connection, id := range s.connections {
		if _, ok := activeIDs[id]; !ok {
			connections = append(connections, connection)
			done = append(done, s.connectionDone[connection])
		}
	}
	s.mu.Unlock()
	closeSSHConnections(connections, done)
	return nil
}

// Reset disables every endpoint and terminates active SSH connections.
func (s *Server) Reset() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.targets = make(map[string]Target)
	connections := make([]net.Conn, 0, len(s.connections))
	done := make([]<-chan struct{}, 0, len(s.connections))
	for connection := range s.connections {
		connections = append(connections, connection)
		done = append(done, s.connectionDone[connection])
	}
	s.mu.Unlock()
	closeSSHConnections(connections, done)
}

func closeSSHConnections(connections []net.Conn, done []<-chan struct{}) {
	for _, connection := range connections {
		_ = connection.Close()
	}
	for _, connectionDone := range done {
		if connectionDone != nil {
			<-connectionDone
		}
	}
}
