//go:build e2e

package dataplane

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/utils/ptr"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	clientapp "github.com/fengqi-dev/kube-loop/internal/app"
	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	clientportforward "github.com/fengqi-dev/kube-loop/internal/client/portforward"
	clientprofile "github.com/fengqi-dev/kube-loop/internal/client/profile"
	clientremote "github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/client/traffic"
	"github.com/fengqi-dev/kube-loop/internal/client/websocketmux"
	controlplanerelayregistry "github.com/fengqi-dev/kube-loop/internal/controlplane/relayregistry"
	"github.com/fengqi-dev/kube-loop/internal/helper"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/protocol/tunnel"
	"github.com/fengqi-dev/kube-loop/internal/singbox"
)

const (
	testIssuer         = "https://control-plane.e2e.invalid"
	testRelay          = "relay-0fbd7cd6c3222a8750011090563d60911c419e7502d47e451a4ee67126be606d"
	testKeyID          = "e2e"
	testPath           = "/tunnel"
	slowConsumerScript = `import socket, time
server = socket.socket()
server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
server.bind(("0.0.0.0", 18080))
server.listen()
while True:
    connection, _ = server.accept()
    connection.sendall(b"R")
    time.sleep(2)
    while connection.recv(65536):
        pass
    connection.sendall(b"OK")
    connection.close()
`
	portForwardEchoScript = `import socket, threading
def serve_tcp_connection(connection):
    try:
        payload = connection.recv(65535)
        connection.sendall(b"tcp:" + payload)
    finally:
        connection.close()

def serve_tcp():
    server = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    server.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    server.bind(("0.0.0.0", 19090))
    server.listen()
    while True:
        connection, _ = server.accept()
        threading.Thread(target=serve_tcp_connection, args=(connection,), daemon=True).start()

def serve_udp():
    server = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    server.bind(("0.0.0.0", 19090))
    while True:
        payload, address = server.recvfrom(65535)
        server.sendto(b"udp:" + payload, address)

threading.Thread(target=serve_udp, daemon=True).start()
serve_tcp()
`
)

func TestGatewayPodRestartRecoversDataPlane(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	harness.StopAllHelperSessions()
	t.Cleanup(harness.StopAllHelperSessions)

	client := kubeClient(t)
	namespace := "kubeloop-dataplane-" + strings.ToLower(uuid.NewString()[:8])
	createNamespace(t, ctx, client, namespace)
	t.Cleanup(func() { deleteNamespace(t, client, namespace) })

	publicKey, privateKey, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	installGateway(t, ctx, client, namespace, publicKey)
	service := waitForGateway(t, ctx, client, namespace)
	unauthorizedNamespace := namespace + "-denied"
	createNamespace(t, ctx, client, unauthorizedNamespace)
	t.Cleanup(func() { deleteNamespace(t, client, unauthorizedNamespace) })
	unauthorizedService := installIsolatedEcho(t, ctx, client, unauthorizedNamespace, "namespace-denied")
	slowService, err := client.CoreV1().Services(namespace).Get(ctx, "slow-consumer", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get slow-consumer Service: %v", err)
	}
	echoService, err := client.CoreV1().Services(namespace).Get(ctx, "port-forward-echo", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get Port Forward echo Service: %v", err)
	}
	revocableService := installIsolatedEcho(t, ctx, client, namespace, "network-revocable")
	echoPod := readyPodByLabel(t, ctx, client, namespace, "app.kubernetes.io/name=port-forward-echo")
	dnsService, err := client.CoreV1().Services("kube-system").Get(ctx, "kube-dns", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get cluster DNS Service: %v", err)
	}
	podCIDRs := clusterPodCIDRs(t, ctx, client)
	// Minikube can retain an old Node.Spec.PodCIDR after its CNI allocation
	// range advances across profile restarts. CIDRs are routing hints only; the
	// observed Pod IP is the exact Gateway authorization target.
	nodeAddress := reachableNodeAddress(t, ctx, client)
	publicAddress := net.JoinHostPort(nodeAddress, fmt.Sprint(service.Spec.Ports[0].NodePort))
	waitForHTTP(t, ctx, "http://"+publicAddress+"/health/ready")
	proxy, err := startLoopbackProxy(ctx, publicAddress)
	if err != nil {
		t.Fatalf("start stable loopback Gateway proxy: %v", err)
	}
	t.Cleanup(func() {
		if err := proxy.Close(); err != nil {
			t.Logf("close stable loopback Gateway proxy: %v", err)
		}
	})
	alternateProxy, err := startLoopbackProxy(ctx, publicAddress)
	if err != nil {
		t.Fatalf("start alternate network path: %v", err)
	}
	t.Cleanup(func() {
		if err := alternateProxy.Close(); err != nil {
			t.Logf("close alternate network path: %v", err)
		}
	})

	spec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: podCIDRs, PodIPs: []string{echoPod.Status.PodIP},
		ServiceIPs: []string{
			service.Spec.ClusterIP, slowService.Spec.ClusterIP, echoService.Spec.ClusterIP,
			revocableService.Spec.ClusterIP,
		},
		DNSServer: dnsService.Spec.ClusterIP, ClusterDomains: []string{"cluster.local"},
	})
	if err != nil {
		t.Fatal(err)
	}
	specHash, err := networkspec.Hash(spec)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	session := clientremote.Session{
		ID: uuid.NewString(), Namespace: namespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: now.Add(10 * time.Minute),
		NetworkSpec: spec, NetworkSpecHash: specHash,
	}
	signer, err := relayticket.NewSigner(testKeyID, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	identityID := uuid.NewString()
	deviceID := "e2e-device-a"
	source := &e2eSessionSource{
		signer: signer, session: session, identityID: identityID, deviceID: deviceID,
		trafficEncryption: new(false),
	}
	controlPlane := startPortForwardControlPlane(
		t, ctx, kubeRESTConfig(t), proxy.Address(), "e2e", identityID, deviceID, session,
	)
	statusEvents := make(chan clientdataplane.StatusEvent, 32)
	tunStarter := &recordingTUNStarter{delegate: clientapp.NewSingboxRuntime(nil)}
	manager, err := clientdataplane.NewManager(source, clientdataplane.Config{
		StartTimeout: 10 * time.Second, RecoveryAttempts: 10, RecoveryBackoff: 200 * time.Millisecond,
		TUNStarter: tunStarter,
		OnStatus: func(event clientdataplane.StatusEvent) {
			statusEvents <- event
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := manager.Shutdown(); err != nil {
			t.Logf("shutdown Data Plane: %v", err)
		}
	})
	serverProfile := controlPlane.profile
	status, err := manager.Connect(ctx, serverProfile, session)
	if err != nil {
		t.Fatalf("connect Data Plane: %v", err)
	}
	target := net.JoinHostPort(service.Spec.ClusterIP, "80")
	if err := requestThroughSOCKS(ctx, status.SOCKSAddress, target); err != nil {
		t.Fatalf("initial cluster request: %v", err)
	}
	gatewayDNSName := "kubeloop-gateway." + namespace + ".svc.cluster.local"
	if err := requestThroughSOCKS(ctx, status.SOCKSAddress, net.JoinHostPort(gatewayDNSName, "80")); err != nil {
		t.Fatalf("initial cluster DNS request: %v", err)
	}
	podTarget := net.JoinHostPort(echoPod.Status.PodIP, "19090")
	if err := assertEchoThroughSOCKS(ctx, status.SOCKSAddress, podTarget); err != nil {
		t.Fatalf("initial PodIP TCP/UDP request: %v", err)
	}
	serviceTarget := net.JoinHostPort(echoService.Spec.ClusterIP, "19090")
	if err := assertEchoThroughSOCKS(ctx, status.SOCKSAddress, serviceTarget); err != nil {
		t.Fatalf("initial same-namespace ServiceIP TCP/UDP request: %v", err)
	}
	revocableTarget := net.JoinHostPort(revocableService.Spec.ClusterIP, "19090")
	if err := wait.PollUntilContextTimeout(
		ctx,
		250*time.Millisecond,
		30*time.Second,
		true,
		func(pollCtx context.Context) (bool, error) {
			return assertEchoThroughSOCKS(pollCtx, status.SOCKSAddress, revocableTarget) == nil, nil
		},
	); err != nil {
		t.Fatalf("initial revocable ServiceIP TCP/UDP request did not become ready: %v", err)
	}
	assertGatewayTargetRejected(
		t, ctx, status.SOCKSAddress,
		net.JoinHostPort(unauthorizedService.Spec.ClusterIP, "19090"),
		"cross-namespace ServiceIP",
	)
	assertGatewayTargetRejected(
		t, ctx, status.SOCKSAddress,
		net.JoinHostPort(
			"namespace-denied."+unauthorizedNamespace+".svc.cluster.local", "19090",
		),
		"cross-namespace DNS",
	)
	portForwards, err := clientportforward.New(controlPlane.remote, manager)
	if err != nil {
		t.Fatalf("create Port Forward manager: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := portForwards.Shutdown(shutdownContext); err != nil {
			t.Logf("shutdown Port Forward manager: %v", err)
		}
	})
	tcpForward, err := portForwards.Start(ctx, serverProfile, session, clientportforward.Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: echoService.Name, Protocol: "tcp", RemotePort: 19090,
		LocalPort: availablePortForwardPort(t, "tcp"),
	})
	if err != nil {
		t.Fatalf("start real TCP Port Forward: %v", err)
	}
	udpForward, err := portForwards.Start(ctx, serverProfile, session, clientportforward.Request{
		ProfileID: serverProfile.ID, Kind: "service", Name: echoService.Name, Protocol: "udp", RemotePort: 19090,
		LocalPort: availablePortForwardPort(t, "udp"),
	})
	if err != nil {
		t.Fatalf("start real UDP Port Forward: %v", err)
	}
	if err := assertPortForwardEcho(ctx, tcpForward.Address, udpForward.Address); err != nil {
		t.Fatalf("initial real TCP/UDP Port Forward: %v", err)
	}
	podTCPForward, err := portForwards.Start(ctx, serverProfile, session, clientportforward.Request{
		ProfileID: serverProfile.ID, Kind: "pod", Name: echoPod.Name, Protocol: "tcp", RemotePort: 19090,
		LocalPort: availablePortForwardPort(t, "tcp"),
	})
	if err != nil {
		t.Fatalf("start real Pod TCP Port Forward: %v", err)
	}
	podUDPForward, err := portForwards.Start(ctx, serverProfile, session, clientportforward.Request{
		ProfileID: serverProfile.ID, Kind: "pod", Name: echoPod.Name, Protocol: "udp", RemotePort: 19090,
		LocalPort: availablePortForwardPort(t, "udp"),
	})
	if err != nil {
		t.Fatalf("start real Pod UDP Port Forward: %v", err)
	}
	if err := assertPortForwardEcho(ctx, podTCPForward.Address, podUDPForward.Address); err != nil {
		t.Fatalf("initial real Pod TCP/UDP Port Forward: %v", err)
	}
	tunStatus, err := manager.StartTUN(ctx, serverProfile.ID)
	if err != nil {
		t.Fatalf("start real TUN: %v", err)
	}
	if tunStatus.Mode != "tun" || tunStatus.SOCKSAddress != status.SOCKSAddress {
		t.Fatalf("real TUN status = %#v", tunStatus)
	}
	helperClient, err := helper.NewClient()
	if err != nil {
		t.Fatalf("create Helper client: %v", err)
	}
	tunSessionID := activeHelperSession(t, ctx, helperClient)
	if err := waitForDirectRequest(ctx, target, 15*time.Second); err != nil {
		t.Fatalf("initial cluster request through real TUN: %v", err)
	}
	if err := waitForDirectRequest(ctx, net.JoinHostPort(gatewayDNSName, "80"), 15*time.Second); err != nil {
		t.Fatalf("cluster DNS request through real TUN: %v", err)
	}
	if err := wait.PollUntilContextTimeout(
		ctx,
		250*time.Millisecond,
		20*time.Second,
		true,
		func(pollCtx context.Context) (bool, error) {
			return assertDirectTCPEcho(pollCtx, podTarget) == nil, nil
		},
	); err != nil {
		t.Fatalf("PodIP request through real TUN did not become ready: %v", err)
	}
	assertSlowConsumerIsolation(
		t, ctx, status.SOCKSAddress,
		net.JoinHostPort(slowService.Spec.ClusterIP, "18080"), target,
	)
	networkGeneration, networkTickets := source.snapshot()
	revokedSpec, err := networkspec.Normalize(networkspec.Spec{
		PodCIDRs: spec.PodCIDRs, PodIPs: spec.PodIPs, ServiceCIDRs: spec.ServiceCIDRs,
		ServiceIPs: []string{service.Spec.ClusterIP, slowService.Spec.ClusterIP, echoService.Spec.ClusterIP},
		DNSServer:  spec.DNSServer, ClusterDomains: spec.ClusterDomains,
	})
	if err != nil {
		t.Fatalf("build refreshed NetworkSpec: %v", err)
	}
	if err := source.replaceNetworkSpec(revokedSpec); err != nil {
		t.Fatalf("stage refreshed NetworkSpec: %v", err)
	}
	proxy.Switch("127.0.0.1:1")
	waitForDataPlaneState(t, ctx, statusEvents, "reconnecting")
	assertPortForwardAddresses(t, portForwards, serverProfile.ID, tcpForward, udpForward, podTCPForward, podUDPForward)
	if err := requestDirect(ctx, target); err == nil {
		t.Fatal("real TUN silently bypassed the isolated Data Plane during network outage")
	}
	if activeHelperSession(t, ctx, helperClient) != tunSessionID {
		t.Fatal("real TUN was reinstalled while the Data Plane was isolated")
	}
	proxy.Switch(alternateProxy.Address())
	previousNetworkSpecHash := status.NetworkSpecHash
	var recoveryStatus clientdataplane.Status
	var recoveryChecks [4]error
	err = wait.PollUntilContextTimeout(
		ctx,
		200*time.Millisecond,
		30*time.Second,
		true,
		func(pollCtx context.Context) (bool, error) {
			generation, tickets := source.snapshot()
			if generation <= networkGeneration || tickets <= networkTickets {
				return false, nil
			}
			refreshedStatus, statusErr := manager.Status(serverProfile.ID)
			if statusErr != nil || refreshedStatus.State != "connected" || refreshedStatus.Mode != "tun" ||
				refreshedStatus.NetworkSpecHash == previousNetworkSpecHash {
				return false, nil
			}
			status = refreshedStatus
			recoveryStatus = refreshedStatus
			recoveryChecks[0] = requestThroughSOCKS(pollCtx, status.SOCKSAddress, target)
			recoveryChecks[1] = requestDirect(pollCtx, target)
			recoveryChecks[2] = assertPortForwardEcho(pollCtx, tcpForward.Address, udpForward.Address)
			recoveryChecks[3] = assertPortForwardEcho(pollCtx, podTCPForward.Address, podUDPForward.Address)
			return recoveryChecks[0] == nil && recoveryChecks[1] == nil && recoveryChecks[2] == nil &&
				recoveryChecks[3] == nil, nil
		},
	)
	if err != nil {
		generation, tickets := source.snapshot()
		t.Fatalf(
			"Data Plane did not recover after switching network path: generation=%d tickets=%d status=%#v checks=%v: %v",
			generation,
			tickets,
			recoveryStatus,
			recoveryChecks,
			err,
		)
	}
	refreshedTunSessionID := activeHelperSession(t, ctx, helperClient)
	if refreshedTunSessionID == tunSessionID {
		t.Fatal("real TUN was not reinstalled after the NetworkSpec changed")
	}
	assertGatewayTargetRejected(t, ctx, status.SOCKSAddress, revocableTarget, "revoked ServiceIP")
	assertGatewayTargetRejected(
		t, ctx, status.SOCKSAddress,
		net.JoinHostPort("network-revocable."+namespace+".svc.cluster.local", "19090"),
		"revoked Service DNS",
	)
	secondSession := session
	secondSession.ID = uuid.NewString()
	secondSession.Generation = 1
	secondSource := &e2eSessionSource{
		signer: signer, session: secondSession, identityID: uuid.NewString(), deviceID: "e2e-device-b",
		trafficEncryption: new(false),
	}
	secondManager, err := clientdataplane.NewManager(secondSource, clientdataplane.Config{
		StartTimeout: 10 * time.Second, RecoveryAttempts: 3, RecoveryBackoff: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	secondStatus, err := secondManager.Connect(ctx, clientprofile.Profile{
		ID: "e2e-b", BaseURL: controlPlane.profile.BaseURL, TunnelPath: testPath,
	}, secondSession)
	if err != nil {
		t.Fatalf("connect second identity: %v", err)
	}
	if err := requestThroughSOCKS(ctx, secondStatus.SOCKSAddress, target); err != nil {
		t.Fatalf("second identity cluster request: %v", err)
	}
	assertCrossSessionRejected(t, ctx, proxy.Address(), source, secondSession.ID, spec)
	if err := secondManager.Shutdown(); err != nil {
		t.Fatalf("shutdown second identity: %v", err)
	}
	initialGeneration, initialTickets := source.snapshot()

	drainStream := startSlowConsumerRequest(
		t, ctx, status.SOCKSAddress, net.JoinHostPort(slowService.Spec.ClusterIP, "18080"),
	)
	oldPod := gatewayPod(t, ctx, client, namespace, "")
	if err := client.CoreV1().Pods(namespace).Delete(ctx, oldPod.Name, metav1.DeleteOptions{}); err != nil {
		t.Fatalf("delete Gateway Pod: %v", err)
	}
	select {
	case err := <-drainStream:
		if err != nil {
			t.Fatalf("active stream did not finish during Gateway drain window: %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("active stream did not finish before the Gateway drain deadline")
	}
	newPod := gatewayPod(t, ctx, client, namespace, string(oldPod.UID))
	if newPod.UID == oldPod.UID {
		t.Fatalf("Gateway Pod UID did not change: %s", oldPod.UID)
	}

	err = wait.PollUntilContextTimeout(
		ctx,
		200*time.Millisecond,
		2*time.Minute,
		true,
		func(pollCtx context.Context) (bool, error) {
			generation, tickets := source.snapshot()
			if generation <= initialGeneration || tickets <= initialTickets {
				return false, nil
			}
			return requestThroughSOCKS(pollCtx, status.SOCKSAddress, target) == nil &&
				requestDirect(pollCtx, target) == nil &&
				requestThroughSOCKS(pollCtx, status.SOCKSAddress, net.JoinHostPort(gatewayDNSName, "80")) == nil &&
				assertEchoThroughSOCKS(pollCtx, status.SOCKSAddress, podTarget) == nil &&
				assertPortForwardEcho(pollCtx, tcpForward.Address, udpForward.Address) == nil &&
				assertPortForwardEcho(pollCtx, podTCPForward.Address, podUDPForward.Address) == nil, nil
		},
	)
	if err != nil {
		generation, tickets := source.snapshot()
		t.Fatalf(
			"Data Plane did not recover through the stable SOCKS endpoint after Gateway restart: generation=%d tickets=%d: %v",
			generation,
			tickets,
			err,
		)
	}
	generation, tickets := source.snapshot()
	if generation <= initialGeneration || tickets <= initialTickets {
		t.Fatalf(
			"recovery did not refresh Session/Ticket: generation %d->%d tickets %d->%d",
			initialGeneration,
			generation,
			initialTickets,
			tickets,
		)
	}
	if activeHelperSession(t, ctx, helperClient) != refreshedTunSessionID {
		t.Fatal("real TUN was reinstalled after Gateway Pod recovery")
	}
	allForwards := []clientportforward.Info{tcpForward, udpForward, podTCPForward, podUDPForward}
	assertPortForwardAddresses(t, portForwards, serverProfile.ID, allForwards...)
	for _, forward := range allForwards {
		assertTrafficBindingActive(
			ctx, t, controlPlane.bindingConfig, namespace, forward.ID, "PortForward",
		)
		if err := portForwards.Pause(ctx, serverProfile.ID, forward.ID); err != nil {
			t.Fatalf("stop real %s %s Port Forward: %v", forward.Kind, forward.Protocol, err)
		}
		assertTrafficBindingPaused(ctx, t, controlPlane.bindingConfig, namespace, forward.ID)
	}
	if err := portForwards.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown Port Forward manager before restore: %v", err)
	}
	restoredPortForwards, err := clientportforward.New(controlPlane.remote, manager)
	if err != nil {
		t.Fatalf("create restored Port Forward manager: %v", err)
	}
	t.Cleanup(func() {
		shutdownContext, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		_ = restoredPortForwards.Shutdown(shutdownContext)
	})
	if err := restoredPortForwards.Restore(ctx, serverProfile, session); err != nil {
		t.Fatalf("restore stopped Port Forwards after client restart: %v", err)
	}
	restored := restoredPortForwards.List(serverProfile.ID)
	if len(restored) != len(allForwards) {
		t.Fatalf("restored Port Forward list = %#v", restored)
	}
	restoredByID := make(map[string]clientportforward.Info, len(restored))
	for _, forward := range restored {
		restoredByID[forward.ID] = forward
	}
	for _, forward := range allForwards {
		item, ok := restoredByID[forward.ID]
		if !ok || item.State != "paused" || item.LocalPort != forward.LocalPort {
			t.Fatalf("restored Port Forward %s = %#v", forward.ID, item)
		}
		resumed, resumeErr := restoredPortForwards.Resume(ctx, serverProfile.ID, forward.ID)
		if resumeErr != nil {
			t.Fatalf("resume restored %s %s Port Forward: %v", forward.Kind, forward.Protocol, resumeErr)
		}
		if resumed.LocalPort != forward.LocalPort || resumed.State != "active" {
			t.Fatalf("resumed Port Forward %s = %#v", forward.ID, resumed)
		}
		assertTrafficBindingActive(
			ctx, t, controlPlane.bindingConfig, namespace, forward.ID, "PortForward",
		)
	}
	if err := assertPortForwardEcho(ctx, tcpForward.Address, udpForward.Address); err != nil {
		t.Fatalf("restored real Service TCP/UDP Port Forward: %v", err)
	}
	if err := assertPortForwardEcho(ctx, podTCPForward.Address, podUDPForward.Address); err != nil {
		t.Fatalf("restored real Pod TCP/UDP Port Forward: %v", err)
	}
	for _, forward := range allForwards {
		if err := restoredPortForwards.Pause(ctx, serverProfile.ID, forward.ID); err != nil {
			t.Fatalf("stop restored %s %s Port Forward: %v", forward.Kind, forward.Protocol, err)
		}
		assertTrafficBindingPaused(ctx, t, controlPlane.bindingConfig, namespace, forward.ID)
		if err := restoredPortForwards.Delete(ctx, serverProfile.ID, forward.ID); err != nil {
			t.Fatalf("delete stopped %s %s Port Forward: %v", forward.Kind, forward.Protocol, err)
		}
		assertTrafficBindingAbsent(ctx, t, controlPlane.bindingConfig, namespace, forward.ID)
	}
	portForwards = restoredPortForwards
	assertPortForwardPortsReleased(t, tcpForward.Address, udpForward.Address)
	assertPortForwardPortsReleased(t, podTCPForward.Address, podUDPForward.Address)
	stopped, err := manager.StopTUN(serverProfile.ID)
	if err != nil || stopped.Mode != "socks" {
		t.Fatalf("stop real TUN: status=%#v error=%v", stopped, err)
	}
	if err := waitForHelperIdle(ctx, helperClient, 15*time.Second); err != nil {
		t.Fatalf("real TUN did not clean up Helper state: %v", err)
	}

	// A local core crash is not a graceful StopTUN call. Inject the same
	// RunningCore Done/Err signal produced by an unexpected process exit; the
	// Data Plane must close the real core and stable SOCKS endpoint, then let the
	// privileged Helper restore TUN routes and split DNS. Platform E2E separately
	// covers an actual SIGKILL of the privileged process.
	crashStatus, err := manager.StartTUN(ctx, serverProfile.ID)
	if err != nil || crashStatus.Mode != "tun" {
		t.Fatalf("restart TUN for client crash: status=%#v err=%v", crashStatus, err)
	}
	crashedCore := tunStarter.Current()
	if crashedCore == nil || crashedCore.InternalDNSPort() == 0 {
		t.Fatalf("recorded TUN core is invalid: %#v", crashedCore)
	}
	dnsPort := crashedCore.InternalDNSPort()
	pingContext, pingCancel := context.WithTimeout(ctx, 5*time.Second)
	helperStatus, err := helperClient.Ping(pingContext)
	pingCancel()
	if err != nil || helperStatus.PID < 1 || len(helperStatus.ActiveSessions) != 1 {
		t.Fatalf("Helper did not report the crash target: status=%#v err=%v", helperStatus, err)
	}
	crashedCore.Crash(errors.New("simulated sing-box process crash"))
	if err := waitForHelperIdle(ctx, helperClient, 30*time.Second); err != nil {
		t.Fatalf("Helper did not clean up crashed TUN: %v", err)
	}
	harness.WaitDNSProxyGone(t, dnsPort, gatewayDNSName)
	harness.AssertClusterDNSGone(t, gatewayDNSName, service.Spec.ClusterIP)
	err = wait.PollUntilContextTimeout(
		ctx,
		100*time.Millisecond,
		15*time.Second,
		true,
		func(pollCtx context.Context) (bool, error) {
			connection, dialErr := (&net.Dialer{}).DialContext(pollCtx, "tcp", status.SOCKSAddress)
			if connection != nil {
				_ = connection.Close()
			}
			return dialErr != nil, nil
		},
	)
	if err != nil {
		t.Fatalf("SOCKS endpoint survived local core crash: %v", err)
	}
}

type loopbackProxy struct {
	listener  net.Listener
	upstream  string
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
	wait      sync.WaitGroup
	mu        sync.Mutex
	closed    bool
	active    map[*proxyConnection]struct{}
}

type proxyConnection struct {
	client   net.Conn
	upstream net.Conn
}

func startLoopbackProxy(parent context.Context, upstream string) (*loopbackProxy, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	proxy := &loopbackProxy{
		listener: listener, upstream: upstream, ctx: ctx, cancel: cancel,
		active: make(map[*proxyConnection]struct{}),
	}
	proxy.wait.Add(1)
	go proxy.accept()
	return proxy, nil
}

func (proxy *loopbackProxy) Address() string { return proxy.listener.Addr().String() }

func (proxy *loopbackProxy) Switch(upstream string) {
	proxy.mu.Lock()
	proxy.upstream = upstream
	active := make([]*proxyConnection, 0, len(proxy.active))
	for connection := range proxy.active {
		active = append(active, connection)
	}
	proxy.mu.Unlock()
	for _, connection := range active {
		_ = connection.client.Close()
		_ = connection.upstream.Close()
	}
}

func (proxy *loopbackProxy) Close() error {
	var result error
	proxy.closeOnce.Do(func() {
		proxy.mu.Lock()
		proxy.closed = true
		active := make([]*proxyConnection, 0, len(proxy.active))
		for connection := range proxy.active {
			active = append(active, connection)
		}
		proxy.mu.Unlock()
		proxy.cancel()
		result = proxy.listener.Close()
		for _, connection := range active {
			_ = connection.client.Close()
			_ = connection.upstream.Close()
		}
		proxy.wait.Wait()
	})
	if result != nil && !strings.Contains(result.Error(), "use of closed network connection") {
		return result
	}
	return nil
}

func (proxy *loopbackProxy) accept() {
	defer proxy.wait.Done()
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		proxy.mu.Lock()
		if proxy.closed {
			proxy.mu.Unlock()
			_ = client.Close()
			continue
		}
		selectedUpstream := proxy.upstream
		proxy.wait.Add(1)
		proxy.mu.Unlock()
		go func() {
			defer proxy.wait.Done()
			defer client.Close()
			dialContext, cancel := context.WithTimeout(proxy.ctx, 5*time.Second)
			upstream, err := (&net.Dialer{}).DialContext(dialContext, "tcp", selectedUpstream)
			cancel()
			if err != nil {
				return
			}
			defer upstream.Close()
			connection := &proxyConnection{client: client, upstream: upstream}
			proxy.mu.Lock()
			if proxy.closed || selectedUpstream != proxy.upstream {
				proxy.mu.Unlock()
				return
			}
			proxy.active[connection] = struct{}{}
			proxy.mu.Unlock()
			defer func() {
				proxy.mu.Lock()
				delete(proxy.active, connection)
				proxy.mu.Unlock()
			}()
			complete := make(chan struct{}, 2)
			go func() { _, _ = io.Copy(upstream, client); complete <- struct{}{} }()
			go func() { _, _ = io.Copy(client, upstream); complete <- struct{}{} }()
			select {
			case <-complete:
			case <-proxy.ctx.Done():
			}
		}()
	}
}

type e2eSessionSource struct {
	mu                sync.Mutex
	signer            *relayticket.Signer
	session           clientremote.Session
	identityID        string
	deviceID          string
	trafficEncryption *bool
	tickets           int
}

type recordingTUNStarter struct {
	delegate clientdataplane.TUNStarter

	mu   sync.Mutex
	core *crashableCore
}

func (starter *recordingTUNStarter) Start(
	ctx context.Context,
	network singbox.NetworkSpec,
	bridgeAddress, namespace string,
	hosts []singbox.HostAlias,
) (singbox.RunningCore, error) {
	core, err := starter.delegate.Start(ctx, network, bridgeAddress, namespace, hosts)
	if err == nil {
		wrapped := newCrashableCore(core)
		starter.mu.Lock()
		starter.core = wrapped
		starter.mu.Unlock()
		return wrapped, nil
	}
	return core, err
}

func (starter *recordingTUNStarter) Current() *crashableCore {
	starter.mu.Lock()
	defer starter.mu.Unlock()
	return starter.core
}

type crashableCore struct {
	singbox.RunningCore
	done chan struct{}
	once sync.Once

	mu       sync.Mutex
	crashErr error
}

func newCrashableCore(core singbox.RunningCore) *crashableCore {
	wrapped := &crashableCore{RunningCore: core, done: make(chan struct{})}
	go func() {
		<-core.Done()
		wrapped.once.Do(func() { close(wrapped.done) })
	}()
	return wrapped
}

func (core *crashableCore) Done() <-chan struct{} { return core.done }

func (core *crashableCore) Err() error {
	core.mu.Lock()
	defer core.mu.Unlock()
	if core.crashErr != nil {
		return core.crashErr
	}
	return core.RunningCore.Err()
}

func (core *crashableCore) Crash(err error) {
	core.mu.Lock()
	core.crashErr = err
	core.mu.Unlock()
	core.once.Do(func() { close(core.done) })
}

func (core *crashableCore) Close() error {
	err := core.RunningCore.Close()
	core.once.Do(func() { close(core.done) })
	return err
}

func (source *e2eSessionSource) RelayTicketSource(string) func(context.Context) (clientremote.RelayTicket, error) {
	return func(context.Context) (clientremote.RelayTicket, error) {
		source.mu.Lock()
		defer source.mu.Unlock()
		now := time.Now().UTC().Truncate(time.Second)
		ticket, err := source.signer.Sign(relayticket.Claims{
			Version:           relayticket.Version,
			Issuer:            testIssuer,
			Audience:          testRelay,
			IdentityID:        source.identityID,
			DeviceID:          source.deviceID,
			SessionID:         source.session.ID,
			SessionGeneration: source.session.Generation,
			Namespace:         source.session.Namespace,
			Operations:        []string{"tunnel"},
			NetworkSpecHash:   source.session.NetworkSpecHash,
			TicketID:          uuid.NewString(),
			IssuedAt:          now.Unix(),
			NotBefore:         now.Unix(),
			ExpiresAt:         now.Add(time.Minute).Unix(),
			TrafficEncryption: source.trafficEncryption,
		})
		if err == nil {
			source.tickets++
		}
		return clientremote.RelayTicket{
			TokenType: relayticket.Type, Ticket: ticket, ExpiresAt: now.Add(time.Minute), DeviceID: source.deviceID,
			TrafficEncryption: source.trafficEncryption,
		}, err
	}
}

func assertCrossSessionRejected(
	t *testing.T,
	ctx context.Context,
	gatewayAddress string,
	source *e2eSessionSource,
	otherSessionID string,
	spec networkspec.Spec,
) {
	t.Helper()
	ticketSource := source.RelayTicketSource("e2e")
	forwarder, err := websocketmux.Start(ctx, websocketmux.ClientConfig{
		DeviceID: source.deviceID,
		URL:      "ws://" + gatewayAddress + testPath, TokenSource: func(ctx context.Context) (string, error) {
			ticket, err := ticketSource(ctx)
			return ticket.Ticket, err
		},
		PoolSize: 1, MaxPhysical: 1, TrafficEncryption: source.trafficEncryption,
	})
	if err != nil {
		t.Fatalf("open cross-Session isolation transport: %v", err)
	}
	defer forwarder.Close()
	connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", forwarder.Address())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	otherToken, err := tunnel.RelaySessionToken(otherSessionID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := tunnel.WriteAuthorizedControlSession(connection, otherToken, spec); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.ReadStatus(
		connection,
	); err == nil ||
		!strings.Contains(err.Error(), "does not match RelayTicket") {
		t.Fatalf("cross-Session protocol token was not rejected: %v", err)
	}
}

func (source *e2eSessionSource) Current(string) (clientremote.Session, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.session, nil
}

func (source *e2eSessionSource) Refresh(context.Context, string) (clientremote.Session, error) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.session.Generation++
	source.session.UpdatedAt = time.Now().UTC()
	source.session.LastHeartbeatAt = source.session.UpdatedAt
	source.session.ExpiresAt = source.session.UpdatedAt.Add(3 * time.Minute)
	return source.session, nil
}

func (source *e2eSessionSource) snapshot() (uint64, int) {
	source.mu.Lock()
	defer source.mu.Unlock()
	return source.session.Generation, source.tickets
}

func (source *e2eSessionSource) replaceNetworkSpec(spec networkspec.Spec) error {
	hash, err := networkspec.Hash(spec)
	if err != nil {
		return err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	source.session.NetworkSpec = spec
	source.session.NetworkSpecHash = hash
	return nil
}

func kubeClient(t *testing.T) kubernetes.Interface {
	t.Helper()
	client, err := kubernetes.NewForConfig(kubeRESTConfig(t))
	if err != nil {
		t.Fatalf("create Kubernetes e2e client: %v", err)
	}
	return client
}

func kubeRESTConfig(t *testing.T) *rest.Config {
	t.Helper()
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{CurrentContext: harness.KubeContext()}
	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loading, overrides).ClientConfig()
	if err != nil {
		t.Fatalf("load Kubernetes e2e context: %v", err)
	}
	return config
}

func createNamespace(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace string) {
	t.Helper()
	_, err := client.CoreV1().
		Namespaces().
		Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Data Plane e2e namespace: %v", err)
	}
}

func deleteNamespace(t *testing.T, client kubernetes.Interface, namespace string) {
	t.Helper()
	if os.Getenv("KUBELOOP_E2E_KEEP") == "1" {
		t.Logf("keeping Data Plane e2e namespace %s", namespace)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := client.CoreV1().Namespaces().Delete(ctx, namespace, metav1.DeleteOptions{}); err != nil {
		t.Logf("delete Data Plane e2e namespace: %v", err)
	}
}

type gatewayTestLimits struct {
	maxSessions          int
	maxSessionsPerUser   int
	maxStreamsPerSession int
}

type relayFixtureAuthenticator struct{}

func (relayFixtureAuthenticator) Authenticate(*http.Request) (relaycontrol.PeerIdentity, error) {
	return relaycontrol.PeerIdentity{
		TrustDomain: "e2e", Namespace: "e2e", ServiceAccount: "gateway", PodUID: "gateway",
	}, nil
}

func startRelayRegistryFixture(t *testing.T, publicKey ed25519.PublicKey) (string, []byte) {
	t.Helper()
	now := time.Now().UTC()
	keys, err := relaycontrol.NewVerificationKeySet(
		1, map[string]ed25519.PublicKey{testKeyID: publicKey}, now.Add(-time.Minute), now.Add(time.Hour),
	)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := controlplanerelayregistry.New(controlplanerelayregistry.Config{
		TicketIssuer: testIssuer, VerificationKeys: keys,
	})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := controlplanerelayregistry.NewHTTPHandler(registry, relayFixtureAuthenticator{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	certificate, certificatePEM := relayFixtureCertificate(t)
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	tlsListener := tls.NewListener(listener, &tls.Config{
		MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate},
	})
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = server.Serve(tlsListener) }()
	t.Cleanup(func() {
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownContext)
	})
	return "https://host.minikube.internal:" + fmt.Sprint(listener.Addr().(*net.TCPAddr).Port), certificatePEM
}

func relayFixtureCertificate(t *testing.T) (tls.Certificate, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(now.UnixNano()), Subject: pkix.Name{CommonName: "host.minikube.internal"},
		DNSNames: []string{"host.minikube.internal"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
		IsCA: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	certificatePEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	privateKeyPEM := pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(privateKey)},
	)
	certificate, err := tls.X509KeyPair(certificatePEM, privateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, certificatePEM
}

func installGateway(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	publicKey ed25519.PublicKey,
) {
	installGatewayWithLimits(t, ctx, client, namespace, publicKey, nil)
}

func installGatewayWithLimits(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
	publicKey ed25519.PublicKey,
	limits *gatewayTestLimits,
) {
	t.Helper()
	const name = "kubeloop-gateway"
	labels := map[string]string{"app.kubernetes.io/name": name, "app.kubernetes.io/component": "data-plane"}
	controlPlaneURL, serverCA := startRelayRegistryFixture(t, publicKey)
	websocketConfig := map[string]any{"trafficEncryption": false}
	if limits != nil {
		websocketConfig["maxSessions"] = limits.maxSessions
		websocketConfig["maxSessionsPerUser"] = limits.maxSessionsPerUser
		websocketConfig["maxStreamsPerSession"] = limits.maxStreamsPerSession
	}
	configuration, err := json.Marshal(map[string]any{
		"controlPlane": map[string]any{},
		"gateway": map[string]any{
			"http": map[string]any{"path": testPath},
			"relay": map[string]any{
				"controlPlaneURL": controlPlaneURL, "endpoint": "wss://relay.e2e.invalid/tunnel",
				"serverCAFile": "/var/run/kubeloop/relay/ca.crt",
			},
			"websocket": websocketConfig, "drainTimeout": "5s",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CoreV1().ConfigMaps(namespace).Create(ctx, &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name}, Data: map[string]string{"kubeloop.yaml": string(configuration)},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Gateway configuration: %v", err)
	}
	_, err = client.CoreV1().Secrets(namespace).Create(ctx, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name + "-relay"}, Data: map[string][]byte{"ca.crt": serverCA},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Gateway RelayTicket key Secret: %v", err)
	}
	hostNetwork := runtime.GOOS == "darwin"
	dnsPolicy := corev1.DNSClusterFirst
	if hostNetwork {
		dnsPolicy = corev1.DNSClusterFirstWithHostNet
	}
	_, err = client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					HostNetwork: hostNetwork, DNSPolicy: dnsPolicy,
					AutomountServiceAccountToken: ptr.To(false), TerminationGracePeriodSeconds: ptr.To[int64](8),
					Containers: []corev1.Container{
						{
							Name:            name,
							Image:           harness.GatewayImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Env: []corev1.EnvVar{
								{Name: "KUBELOOP_GATEWAY_CONFIG_FILE", Value: "/etc/kubeloop/gateway/kubeloop.yaml"},
								{Name: "KUBELOOP_POD_IP", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP"},
								}},
							},
							Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{
									Path: "/health/ready", Port: intstr.FromString("http"),
								}},
								PeriodSeconds:    1,
								FailureThreshold: 30,
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "config", MountPath: "/etc/kubeloop/gateway", ReadOnly: true},
								{Name: "relay", MountPath: "/var/run/kubeloop/relay", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{
							LocalObjectReference: corev1.LocalObjectReference{Name: name},
						}}},
						{Name: "relay", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{
							SecretName: name + "-relay",
						}}},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Gateway Deployment: %v", err)
	}
	_, err = client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort, Selector: labels,
			Ports: []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http")}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Gateway NodePort Service: %v", err)
	}
	slowLabels := map[string]string{"app.kubernetes.io/name": "slow-consumer"}
	_, err = client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "slow-consumer", Labels: slowLabels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: slowLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: slowLabels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name: "slow-consumer", Image: "python:3.12-alpine", ImagePullPolicy: corev1.PullIfNotPresent,
						Command: []string{"python", "-u", "-c", slowConsumerScript},
						Ports:   []corev1.ContainerPort{{Name: "slow", ContainerPort: 18080}},
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create slow-consumer Deployment: %v", err)
	}
	_, err = client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "slow-consumer", Labels: slowLabels},
		Spec: corev1.ServiceSpec{
			Selector: slowLabels,
			Ports:    []corev1.ServicePort{{Name: "slow", Port: 18080, TargetPort: intstr.FromString("slow")}},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create slow-consumer Service: %v", err)
	}
	echoLabels := map[string]string{"app.kubernetes.io/name": "port-forward-echo"}
	_, err = client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "port-forward-echo", Labels: echoLabels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: echoLabels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: echoLabels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{
						{
							Name:            "port-forward-echo",
							Image:           "python:3.12-alpine",
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         []string{"python", "-u", "-c", portForwardEchoScript},
							Ports: []corev1.ContainerPort{
								{Name: "tcp", ContainerPort: 19090, Protocol: corev1.ProtocolTCP},
								{Name: "udp", ContainerPort: 19090, Protocol: corev1.ProtocolUDP},
							},
						},
					},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Port Forward echo Deployment: %v", err)
	}
	_, err = client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "port-forward-echo", Labels: echoLabels},
		Spec: corev1.ServiceSpec{
			Selector: echoLabels,
			Ports: []corev1.ServicePort{
				{Name: "tcp", Port: 19090, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("tcp")},
				{Name: "udp", Port: 19090, Protocol: corev1.ProtocolUDP, TargetPort: intstr.FromString("udp")},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create Port Forward echo Service: %v", err)
	}
}

func installIsolatedEcho(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace, name string,
) *corev1.Service {
	t.Helper()
	labels := map[string]string{"app.kubernetes.io/name": name}
	_, err := client.AppsV1().Deployments(namespace).Create(ctx, &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1), Selector: &metav1.LabelSelector{MatchLabels: labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					AutomountServiceAccountToken: ptr.To(false),
					Containers: []corev1.Container{{
						Name: name, Image: "python:3.12-alpine", ImagePullPolicy: corev1.PullIfNotPresent,
						Command: []string{"python", "-u", "-c", portForwardEchoScript},
						ReadinessProbe: &corev1.Probe{
							ProbeHandler: corev1.ProbeHandler{
								TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromString("tcp")},
							},
							PeriodSeconds:    1,
							FailureThreshold: 30,
						},
						Ports: []corev1.ContainerPort{
							{Name: "tcp", ContainerPort: 19090, Protocol: corev1.ProtocolTCP},
							{Name: "udp", ContainerPort: 19090, Protocol: corev1.ProtocolUDP},
						},
					}},
				},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create cross-namespace echo Deployment: %v", err)
	}
	service, err := client.CoreV1().Services(namespace).Create(ctx, &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{Name: "tcp", Port: 19090, Protocol: corev1.ProtocolTCP, TargetPort: intstr.FromString("tcp")},
				{Name: "udp", Port: 19090, Protocol: corev1.ProtocolUDP, TargetPort: intstr.FromString("udp")},
			},
		},
	}, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create cross-namespace echo Service: %v", err)
	}
	readyPodByLabel(t, ctx, client, namespace, "app.kubernetes.io/name="+name)
	return service
}

func waitForGateway(t *testing.T, ctx context.Context, client kubernetes.Interface, namespace string) *corev1.Service {
	t.Helper()
	err := wait.PollUntilContextTimeout(
		ctx,
		500*time.Millisecond,
		2*time.Minute,
		true,
		func(pollCtx context.Context) (bool, error) {
			gateway, err := client.AppsV1().Deployments(namespace).Get(pollCtx, "kubeloop-gateway", metav1.GetOptions{})
			if err != nil || gateway.Status.AvailableReplicas != 1 {
				return false, nil
			}
			slow, err := client.AppsV1().Deployments(namespace).Get(pollCtx, "slow-consumer", metav1.GetOptions{})
			if err != nil || slow.Status.AvailableReplicas != 1 {
				return false, nil
			}
			echo, err := client.AppsV1().Deployments(namespace).Get(pollCtx, "port-forward-echo", metav1.GetOptions{})
			return err == nil && echo.Status.AvailableReplicas == 1, nil
		},
	)
	if err != nil {
		t.Fatalf("wait for Gateway Deployment: %v", err)
	}
	service, err := client.CoreV1().Services(namespace).Get(ctx, "kubeloop-gateway", metav1.GetOptions{})
	if err != nil || len(service.Spec.Ports) != 1 || service.Spec.Ports[0].NodePort == 0 {
		t.Fatalf("load Gateway NodePort Service: %#v, %v", service, err)
	}
	return service
}

func gatewayPod(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace, previousUID string,
) corev1.Pod {
	t.Helper()
	var found corev1.Pod
	err := wait.PollUntilContextTimeout(
		ctx,
		500*time.Millisecond,
		2*time.Minute,
		true,
		func(pollCtx context.Context) (bool, error) {
			pods, err := client.CoreV1().
				Pods(namespace).
				List(pollCtx, metav1.ListOptions{LabelSelector: "app.kubernetes.io/name=kubeloop-gateway"})
			if err != nil {
				return false, nil
			}
			for _, pod := range pods.Items {
				if string(pod.UID) == previousUID || pod.DeletionTimestamp != nil || !podReady(pod) {
					continue
				}
				found = pod
				return true, nil
			}
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("wait for replacement Gateway Pod: %v", err)
	}
	return found
}

func podReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func readyPodByLabel(
	t *testing.T,
	ctx context.Context,
	client kubernetes.Interface,
	namespace, selector string,
) corev1.Pod {
	t.Helper()
	var found corev1.Pod
	err := wait.PollUntilContextTimeout(
		ctx,
		250*time.Millisecond,
		time.Minute,
		true,
		func(pollCtx context.Context) (bool, error) {
			pods, err := client.CoreV1().Pods(namespace).List(pollCtx, metav1.ListOptions{LabelSelector: selector})
			if err != nil {
				return false, nil
			}
			for _, pod := range pods.Items {
				if pod.DeletionTimestamp == nil && pod.Status.PodIP != "" && podReady(pod) {
					found = pod
					return true, nil
				}
			}
			return false, nil
		},
	)
	if err != nil {
		t.Fatalf("wait for ready Pod matching %q: %v", selector, err)
	}
	return found
}

func clusterPodCIDRs(t *testing.T, ctx context.Context, client kubernetes.Interface) []string {
	t.Helper()
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list nodes for Pod CIDRs: %v", err)
	}
	seen := make(map[string]struct{})
	var result []string
	for _, node := range nodes.Items {
		cidrs := append([]string(nil), node.Spec.PodCIDRs...)
		if len(cidrs) == 0 && node.Spec.PodCIDR != "" {
			cidrs = []string{node.Spec.PodCIDR}
		}
		for _, cidr := range cidrs {
			if _, exists := seen[cidr]; cidr == "" || exists {
				continue
			}
			seen[cidr] = struct{}{}
			result = append(result, cidr)
		}
	}
	if len(result) == 0 {
		t.Fatal("Kubernetes nodes did not advertise any Pod CIDR")
	}
	return result
}

func reachableNodeAddress(t *testing.T, ctx context.Context, client kubernetes.Interface) string {
	t.Helper()
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil || len(nodes.Items) == 0 {
		t.Fatalf("list Kubernetes nodes: %v", err)
	}
	for _, addressType := range []corev1.NodeAddressType{corev1.NodeExternalIP, corev1.NodeInternalIP} {
		for _, node := range nodes.Items {
			for _, address := range node.Status.Addresses {
				if address.Type == addressType && net.ParseIP(address.Address) != nil {
					return address.Address
				}
			}
		}
	}
	t.Fatal("Kubernetes node has no reachable IP address")
	return ""
}

func waitForHTTP(t *testing.T, ctx context.Context, target string) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}}
	err := wait.PollUntilContextTimeout(
		ctx,
		500*time.Millisecond,
		time.Minute,
		true,
		func(context.Context) (bool, error) {
			response, err := client.Get(target)
			if err != nil {
				return false, nil
			}
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
			return response.StatusCode == http.StatusOK, nil
		},
	)
	if err != nil {
		t.Fatalf("wait for Gateway endpoint %s: %v", target, err)
	}
}

func requestThroughSOCKS(ctx context.Context, socksAddress, target string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	connection, err := (traffic.Dialer{Endpoint: traffic.Endpoint{Address: socksAddress}}).DialContext(
		requestCtx,
		"tcp",
		target,
	)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := fmt.Fprintf(
		connection,
		"GET /health/live HTTP/1.1\r\nHost: kubeloop-gateway\r\nConnection: close\r\n\r\n",
	); err != nil {
		return err
	}
	writeCloser, ok := connection.(interface{ CloseWrite() error })
	if !ok {
		return errors.New("SOCKS connection does not support TCP half-close")
	}
	if err := writeCloser.CloseWrite(); err != nil {
		return fmt.Errorf("half-close Gateway health request: %w", err)
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodGet})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Gateway health response status %d", response.StatusCode)
	}
	return nil
}

func assertEchoThroughSOCKS(ctx context.Context, socksAddress, target string) error {
	dialer := traffic.Dialer{Endpoint: traffic.Endpoint{Address: socksAddress}}
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tcpConnection, err := dialer.DialContext(requestContext, "tcp", target)
	if err != nil {
		return fmt.Errorf("dial PodIP TCP through SOCKS: %w", err)
	}
	defer tcpConnection.Close()
	_ = tcpConnection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := tcpConnection.Write([]byte("podip")); err != nil {
		return fmt.Errorf("write PodIP TCP through SOCKS: %w", err)
	}
	tcpResponse := make([]byte, len("tcp:podip"))
	if _, err := io.ReadFull(tcpConnection, tcpResponse); err != nil {
		return fmt.Errorf("read PodIP TCP through SOCKS: %w", err)
	}
	if string(tcpResponse) != "tcp:podip" {
		return fmt.Errorf("unexpected PodIP TCP response %q", tcpResponse)
	}

	udpConnection, err := dialer.DialContext(requestContext, "udp", target)
	if err != nil {
		return fmt.Errorf("dial PodIP UDP through SOCKS: %w", err)
	}
	defer udpConnection.Close()
	_ = udpConnection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := udpConnection.Write([]byte("podip")); err != nil {
		return fmt.Errorf("write PodIP UDP through SOCKS: %w", err)
	}
	udpResponse := make([]byte, 64)
	count, err := udpConnection.Read(udpResponse)
	if err != nil {
		return fmt.Errorf("read PodIP UDP through SOCKS: %w", err)
	}
	if string(udpResponse[:count]) != "udp:podip" {
		return fmt.Errorf("unexpected PodIP UDP response %q", udpResponse[:count])
	}
	return nil
}

func assertGatewayTargetRejected(
	t *testing.T,
	ctx context.Context,
	socksAddress, target, label string,
) {
	t.Helper()
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := assertEchoThroughSOCKS(requestContext, socksAddress, target); err == nil {
		t.Fatalf("Gateway allowed denied %s target %s", label, target)
	}
}

func assertDirectTCPEcho(ctx context.Context, target string) error {
	requestContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	connection, err := (&net.Dialer{}).DialContext(requestContext, "tcp", target)
	if err != nil {
		return err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := connection.Write([]byte("tun-podip")); err != nil {
		return err
	}
	response := make([]byte, len("tcp:tun-podip"))
	if _, err := io.ReadFull(connection, response); err != nil {
		return err
	}
	if string(response) != "tcp:tun-podip" {
		return fmt.Errorf("unexpected direct PodIP response %q", response)
	}
	return nil
}

func requestDirect(ctx context.Context, target string) error {
	requestCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, "http://"+target+"/health/live", nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			Proxy:             nil,
			DisableKeepAlives: true,
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("Gateway health response status %d", response.StatusCode)
	}
	return nil
}

func waitForDirectRequest(ctx context.Context, target string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(
		ctx,
		200*time.Millisecond,
		timeout,
		true,
		func(pollCtx context.Context) (bool, error) {
			return requestDirect(pollCtx, target) == nil, nil
		},
	)
}

func waitForDataPlaneState(
	t *testing.T,
	ctx context.Context,
	events <-chan clientdataplane.StatusEvent,
	want string,
) clientdataplane.StatusEvent {
	t.Helper()
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	for {
		select {
		case event := <-events:
			if event.Status.State == want {
				return event
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-timer.C:
			t.Fatalf("timed out waiting for Data Plane state %q", want)
		}
	}
}

func activeHelperSession(t *testing.T, ctx context.Context, client *helper.Client) string {
	t.Helper()
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := client.Ping(pingCtx)
	if err != nil {
		t.Fatalf("ping Helper: %v", err)
	}
	if len(response.ActiveSessions) != 1 {
		t.Fatalf("Helper active sessions = %v, want one TUN", response.ActiveSessions)
	}
	return response.ActiveSessions[0]
}

func waitForHelperIdle(ctx context.Context, client *helper.Client, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(
		ctx,
		100*time.Millisecond,
		timeout,
		true,
		func(pollCtx context.Context) (bool, error) {
			response, err := client.Ping(pollCtx)
			if err != nil {
				return false, nil
			}
			return len(response.ActiveSessions) == 0, nil
		},
	)
}

func startSlowConsumerRequest(t *testing.T, ctx context.Context, socksAddress, target string) <-chan error {
	t.Helper()
	requestCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	connection, err := (traffic.Dialer{Endpoint: traffic.Endpoint{Address: socksAddress}}).DialContext(
		requestCtx,
		"tcp",
		target,
	)
	if err != nil {
		cancel()
		t.Fatalf("connect drain-window consumer: %v", err)
	}
	_ = connection.SetDeadline(time.Now().Add(12 * time.Second))
	ready := make([]byte, 1)
	if _, err := io.ReadFull(connection, ready); err != nil || string(ready) != "R" {
		cancel()
		_ = connection.Close()
		t.Fatalf("wait for drain-window consumer: ready=%q err=%v", ready, err)
	}
	result := make(chan error, 1)
	go func() {
		defer cancel()
		defer connection.Close()
		if _, err := io.CopyN(connection, zeroReader{}, 1<<20); err != nil {
			result <- err
			return
		}
		writer, ok := connection.(interface{ CloseWrite() error })
		if !ok {
			result <- errors.New("drain-window connection does not support half-close")
			return
		}
		if err := writer.CloseWrite(); err != nil {
			result <- err
			return
		}
		response := make([]byte, 2)
		_, err := io.ReadFull(connection, response)
		if err == nil && string(response) != "OK" {
			err = fmt.Errorf("drain-window response %q", response)
		}
		result <- err
	}()
	return result
}

func assertSlowConsumerIsolation(t *testing.T, ctx context.Context, socksAddress, slowTarget, fastTarget string) {
	t.Helper()
	testCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	connection, err := (traffic.Dialer{Endpoint: traffic.Endpoint{Address: socksAddress}}).DialContext(
		testCtx,
		"tcp",
		slowTarget,
	)
	if err != nil {
		t.Fatalf("connect slow cluster consumer: %v", err)
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(12 * time.Second))
	ready := make([]byte, 1)
	if _, err := io.ReadFull(connection, ready); err != nil || string(ready) != "R" {
		t.Fatalf("wait for slow cluster consumer: ready=%q err=%v", ready, err)
	}
	slowResult := make(chan error, 1)
	go func() {
		if _, err := io.CopyN(connection, zeroReader{}, 16<<20); err != nil {
			slowResult <- err
			return
		}
		writer := connection.(interface{ CloseWrite() error })
		if err := writer.CloseWrite(); err != nil {
			slowResult <- err
			return
		}
		response := make([]byte, 2)
		_, err := io.ReadFull(connection, response)
		if err == nil && string(response) != "OK" {
			err = fmt.Errorf("slow consumer response %q", response)
		}
		slowResult <- err
	}()
	select {
	case err := <-slowResult:
		t.Fatalf("slow consumer did not hold its stream: %v", err)
	case <-time.After(150 * time.Millisecond):
	}

	started := time.Now()
	if err := requestThroughSOCKS(testCtx, socksAddress, fastTarget); err != nil {
		t.Fatalf("sibling stream blocked by slow consumer: %v", err)
	}
	if elapsed := time.Since(started); elapsed >= 1500*time.Millisecond {
		t.Fatalf("sibling stream took %s while another consumer was stalled", elapsed)
	}
	select {
	case err := <-slowResult:
		if err != nil {
			t.Fatalf("slow consumer stream did not recover: %v", err)
		}
	case <-testCtx.Done():
		t.Fatal("slow consumer stream did not complete")
	}
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	clear(buffer)
	return len(buffer), nil
}
