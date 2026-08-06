package podssh

import (
	"fmt"
	"os"
	"slices"

	"github.com/kballard/go-shellquote"
)

func (s *Server) info(target Target) Info {
	args := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull,
		"-o", "LogLevel=ERROR",
	}
	if s.clientIdentityPath != "" {
		args = append(args, "-i", s.clientIdentityPath)
	}
	args = append(args, fmt.Sprintf("%s@%s", target.Container, target.IP))
	return Info{
		ID: targetID(target), Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container, IP: target.IP, Port: DefaultPort,
		Command: shellquote.Join(args...),
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
