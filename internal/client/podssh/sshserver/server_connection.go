package sshserver

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"sync"

	"golang.org/x/crypto/ssh"
)

// HostTCP is installed before the normal Service intercept handler.
func (s *Server) HostTCP(host string, port uint16) (func(net.Conn), bool) {
	return s.HostTCPForContext("", host, port)
}

// HostTCPForContext claims PodIP:22 only for the selected Server Profile. Pod
// address ranges commonly overlap between clusters, so each Data Plane bridge
// must resolve endpoints within its own Profile.
func (s *Server) HostTCPForContext(contextName, host string, port uint16) (func(net.Conn), bool) {
	if s == nil || port != DefaultPort {
		return nil, false
	}
	ip, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return nil, false
	}
	s.mu.RLock()
	var target Target
	ok := false
	for _, candidate := range s.targets {
		if candidate.IP == ip.Unmap().String() && (contextName == "" || candidate.Context == contextName) {
			target = candidate
			ok = true
			break
		}
	}
	s.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return func(connection net.Conn) {
		s.serveConnection(connection, target)
	}, true
}

func (s *Server) serveConnection(raw net.Conn, target Target) {
	done := make(chan struct{})
	s.mu.Lock()
	s.connections[raw] = targetID(target)
	s.connectionDone[raw] = done
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.connections, raw)
		delete(s.connectionDone, raw)
		s.mu.Unlock()
		_ = raw.Close()
		close(done)
	}()

	signer, err := s.signer()
	if err != nil {
		return
	}
	authorizedKeys, err := s.authorizedClientKeys()
	if err != nil {
		return
	}
	config := &ssh.ServerConfig{
		ServerVersion: "SSH-2.0-KubeLoop",
		PublicKeyCallback: func(
			metadata ssh.ConnMetadata,
			key ssh.PublicKey,
		) (*ssh.Permissions, error) {
			if _, ok := targetForLogin(target, metadata.User()); !ok {
				return nil, fmt.Errorf(
					"unknown container %q in Pod %s/%s",
					metadata.User(), target.Namespace, target.Pod,
				)
			}
			for _, authorizedKey := range authorizedKeys {
				if bytes.Equal(key.Marshal(), authorizedKey.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errors.New("unknown Pod SSH client key")
		},
	}
	config.AddHostKey(signer)
	connection, channels, requests, err := ssh.NewServerConn(raw, config)
	if err != nil {
		return
	}
	var handlers sync.WaitGroup
	defer handlers.Wait()
	defer func() {
		_ = connection.Close() // The SSH handler owns the accepted connection and has no result channel.
	}()
	selectedTarget, ok := targetForLogin(target, connection.User())
	if !ok {
		return
	}
	handlers.Go(func() { ssh.DiscardRequests(requests) })
	for channelRequest := range channels {
		if channelRequest.ChannelType() != "session" {
			_ = channelRequest.Reject(ssh.UnknownChannelType, "only session channels are supported")
			continue
		}
		channel, channelRequests, err := channelRequest.Accept()
		if err != nil {
			continue
		}
		handlers.Go(func() { s.serveSession(channel, channelRequests, selectedTarget) })
	}
}
