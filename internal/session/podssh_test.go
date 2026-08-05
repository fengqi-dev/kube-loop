package session

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/cluster"
	"github.com/fengqi-dev/kube-loop/internal/podssh"
	"golang.org/x/crypto/ssh"
)

type sessionSSHExecutor struct{}

func (sessionSSHExecutor) Exec(
	context.Context,
	podssh.Target,
	[]string,
	podssh.Streams,
) error {
	return nil
}

func TestEnablePodSSHUsesFirstContainerAndActivePodIP(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		podSSH: podssh.NewServer(sessionSSHExecutor{}, podssh.WithSigner(signer)),
		stateHub: newStateHub(State{
			Phase: PhaseConnected, Mode: ConnectionModeTUN, Context: "dev",
			Capabilities: &cluster.Capabilities{PodExec: true},
			Pods: []cluster.PodInfo{{
				Name: "api-123", Namespace: "default", Ready: true, IP: "10.244.1.9",
				Containers: []string{"api", "sidecar"},
			}},
			UpdatedAt: time.Now(),
		}),
	}

	info, err := manager.EnablePodSSH(podssh.EnableRequest{
		Context: "dev", Namespace: "default", Pod: "api-123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if info.IP != "10.244.1.9" || info.Port != 22 || info.Container != "api" {
		t.Fatalf("unexpected endpoint: %#v", info)
	}
	if info.Command != "ssh api@10.244.1.9" {
		t.Fatalf("command=%q", info.Command)
	}
	if len(manager.ListPodSSH()) != 1 {
		t.Fatal("enabled endpoint was not listed")
	}
	if err := manager.DisablePodSSH(info.ID); err != nil {
		t.Fatal(err)
	}
	if len(manager.ListPodSSH()) != 0 {
		t.Fatal("disabled endpoint remained listed")
	}
}

func TestEnablePodSSHRequiresTUNAndPodExec(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	manager := &Manager{
		podSSH: podssh.NewServer(sessionSSHExecutor{}, podssh.WithSigner(signer)),
		stateHub: newStateHub(State{
			Phase: PhaseConnected, Mode: ConnectionModeSOCKS, Context: "dev",
			UpdatedAt: time.Now(),
		}),
	}
	if _, err := manager.EnablePodSSH(podssh.EnableRequest{
		Namespace: "default", Pod: "api",
	}); err == nil {
		t.Fatal("SOCKS mode unexpectedly enabled direct Pod-IP SSH")
	}
	manager.stateHub = newStateHub(State{
		Phase: PhaseConnected, Mode: ConnectionModeTUN, Context: "dev",
		Capabilities: &cluster.Capabilities{PodExec: false},
		UpdatedAt:    time.Now(),
	})
	if _, err := manager.EnablePodSSH(podssh.EnableRequest{
		Namespace: "default", Pod: "api",
	}); err == nil {
		t.Fatal("missing pods/exec permission unexpectedly enabled Pod SSH")
	}
}

func TestSyncDefaultPodSSHExposesReadyPodsOnlyInTUNMode(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	state := State{
		Phase: PhaseConnected, Mode: ConnectionModeTUN, Context: "dev",
		Capabilities: &cluster.Capabilities{PodExec: true},
		UpdatedAt:    time.Now(),
	}
	manager := &Manager{
		podSSH:   podssh.NewServer(sessionSSHExecutor{}, podssh.WithSigner(signer)),
		stateHub: newStateHub(state),
	}
	pods := []cluster.PodInfo{
		{
			Name: "api", Namespace: "default", Ready: true, IP: "10.244.1.9",
			Containers: []string{"api", "sidecar"},
		},
		{
			Name: "starting", Namespace: "default", Ready: false, IP: "10.244.1.10",
			Containers: []string{"api"},
		},
	}

	manager.syncDefaultPodSSH(state, pods)
	items := manager.ListPodSSH()
	if len(items) != 1 || items[0].Pod != "api" || items[0].Container != "api" {
		t.Fatalf("default endpoints=%#v", items)
	}
	state.Mode = ConnectionModeSOCKS
	manager.syncDefaultPodSSH(state, pods)
	if len(manager.ListPodSSH()) != 0 {
		t.Fatal("SOCKS mode retained direct Pod-IP SSH endpoints")
	}
}
