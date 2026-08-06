package podssh

import (
	"fmt"
	"slices"

	"github.com/kballard/go-shellquote"
)

func (s *Server) info(target Target) Info {
	command := "ssh "
	if s.clientIdentityPath != "" {
		command += "-i " + shellquote.Join(s.clientIdentityPath) + " "
	}
	command += fmt.Sprintf("%s@%s", target.Container, target.IP)
	return Info{
		ID: targetID(target), Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container, IP: target.IP, Port: DefaultPort,
		Command: command,
	}
}

func targetForLogin(target Target, login string) (Target, bool) {
	containers := target.Containers
	if len(containers) == 0 {
		containers = []string{target.Container}
	}
	if !contains(containers, login) {
		return Target{}, false
	}
	target.Container = login
	return target, true
}

func targetID(target Target) string {
	return podIdentity(target.Context, target.Namespace, target.Pod)
}

func podIdentity(contextName, namespace, pod string) string {
	return contextName + "/" + namespace + "/" + pod
}

func contains(items []string, wanted string) bool {
	return slices.Contains(items, wanted)
}
