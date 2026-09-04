package sshserver

import (
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/kballard/go-shellquote"
)

func (s *Server) info(target Target) Info {
	return s.infoAt(target, target.IP, DefaultPort)
}

func (s *Server) infoAt(target Target, host string, port uint16) Info {
	args := []string{
		"ssh",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull,
		"-o", "LogLevel=ERROR",
	}
	if s.clientIdentityPath != "" {
		args = append(args, "-i", s.clientIdentityPath)
	}
	if port != DefaultPort {
		args = append(args, "-p", strconv.Itoa(int(port)))
	}
	args = append(args, fmt.Sprintf("%s@%s", target.Container, host))
	return Info{
		ID: targetID(target), Context: target.Context, Namespace: target.Namespace,
		Pod: target.Pod, Container: target.Container, IP: host, Port: port,
		Command: shellquote.Join(args...),
	}
}

func targetForLogin(target Target, login string) (Target, bool) {
	containers := target.Containers
	if len(containers) == 0 {
		containers = []string{target.Container}
	}
	if !slices.Contains(containers, login) {
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
