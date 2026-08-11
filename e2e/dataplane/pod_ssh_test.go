//go:build e2e

package dataplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/client/credentials"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/client/podssh"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
	"github.com/fengqi-dev/kube-loop/internal/client/remote"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/execapi"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	"github.com/fengqi-dev/kube-loop/internal/socksbridge"
	"github.com/google/uuid"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type e2ePodSSHSessionSource struct {
	profileID string
	session   remote.Session
}

type e2ePodSSHHostTCPRegistrar struct {
	mu      sync.Mutex
	handler socksbridge.HostTCPHandler
}

func (registrar *e2ePodSSHHostTCPRegistrar) SetHostTCPHandler(
	_ string,
	handler socksbridge.HostTCPHandler,
) error {
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	registrar.handler = handler
	return nil
}

func (registrar *e2ePodSSHHostTCPRegistrar) dial(host string, port uint16) (net.Conn, error) {
	registrar.mu.Lock()
	handler := registrar.handler
	registrar.mu.Unlock()
	if handler == nil {
		return nil, errors.New("Pod SSH host interception is not registered")
	}
	serve, ok := handler(host, port)
	if !ok {
		return nil, fmt.Errorf("Pod SSH host interception rejected %s:%d", host, port)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	go func() {
		defer listener.Close()
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			serve(connection)
		}
	}()
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		_ = listener.Close()
	}
	return connection, err
}

func (source e2ePodSSHSessionSource) Current(profileID string) (remote.Session, error) {
	if profileID != source.profileID {
		return remote.Session{}, errors.New("unknown Pod SSH E2E profile")
	}
	return source.session, nil
}

func TestRealPodSSHThroughGatewayAndLocalIdentityIsolation(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	kubeClient := kubeClient(t)
	if err := harness.EnsureEchoWorkload(ctx, kubeClient); err != nil {
		t.Fatalf("ensure real Pod SSH fixture: %v", err)
	}
	pods, err := kubeClient.CoreV1().Pods(harness.EchoNamespace).List(ctx, metav1.ListOptions{LabelSelector: "app=kubeloop-e2e-echo"})
	if err != nil || len(pods.Items) != 1 {
		t.Fatalf("find real Pod SSH fixture: pods=%d err=%v", len(pods.Items), err)
	}
	pod := pods.Items[0]
	containers := make([]string, 0, len(pod.Spec.Containers))
	for _, container := range pod.Spec.Containers {
		containers = append(containers, container.Name)
	}

	stateStore, err := storage.Open(ctx, storage.Config{
		Backend: storage.BackendSQLite, SQLitePath: filepath.Join(t.TempDir(), "v2-pod-ssh.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stateStore.Close() })
	now := time.Now().UTC()
	principalID, familyID, sessionID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	deviceID := "e2e-pod-ssh-device"
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "e2e", ExternalID: "pod-ssh", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := stateStore.TokenFamilies().Create(ctx, storage.TokenFamily{
		ID: familyID, PrincipalID: principalID, DeviceID: deviceID,
		RefreshTokenHash: bytes.Repeat([]byte{7}, 32), CreatedAt: now, ExpiresAt: now.Add(5 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	network, err := networkspec.Normalize(networkspec.Spec{ServiceIPs: []string{"10.96.0.10"}})
	if err != nil {
		t.Fatal(err)
	}
	networkJSON, err := networkspec.CanonicalJSON(network)
	if err != nil {
		t.Fatal(err)
	}
	networkHash, err := networkspec.Hash(network)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(5 * time.Minute)
	if err := stateStore.Sessions().Create(ctx, storage.Session{
		ID: sessionID, PrincipalID: principalID, DeviceID: deviceID, ClusterID: "minikube",
		Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		NetworkSpec: networkJSON, NetworkSpecHash: networkHash,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatal(err)
	}
	provider, err := controllerkubernetes.NewForRESTConfig(kubeRESTConfig(t), controllerkubernetes.Config{})
	if err != nil {
		t.Fatal(err)
	}
	executor, err := execapi.NewKubernetesExecutor(provider)
	if err != nil {
		t.Fatal(err)
	}
	principal := controller.Principal{
		Subject: principalID, DeviceID: deviceID, FamilyID: familyID, AccessExpiresAt: expiresAt,
	}
	activeSession := sessionapi.ActiveSession{
		ID: sessionID, Namespace: harness.EchoNamespace, Generation: 1,
		ExpiresAt: expiresAt, NetworkSpecHash: networkHash,
	}
	running := startExecController(t, "127.0.0.1:0", stateStore, executor, principal, activeSession)
	t.Cleanup(func() { running.Stop(t) })
	serverProfile := profile.Profile{ID: "e2e-pod-ssh", BaseURL: "http://" + running.Address()}
	credentialStore := &e2eCredentialStore{
		profileID: serverProfile.ID,
		credential: credentials.Credential{
			TokenType: "Bearer", AccessToken: execLifecycleAccessToken, AccessExpiresAt: expiresAt,
			RefreshToken: "unused", RefreshExpiresAt: expiresAt, DeviceID: deviceID,
		},
	}
	remoteClient, err := remote.New(credentialStore, e2eTokenRefresher{}, remote.Config{})
	if err != nil {
		t.Fatal(err)
	}
	remoteSession := remote.Session{
		ID: sessionID, Namespace: harness.EchoNamespace, State: "active", Generation: 1,
		CreatedAt: now, UpdatedAt: now, LastHeartbeatAt: now, ExpiresAt: expiresAt,
		NetworkSpec: network, NetworkSpecHash: networkHash,
	}
	_, privateKeyA, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signerA, err := ssh.NewSignerFromKey(privateKeyA)
	if err != nil {
		t.Fatal(err)
	}
	_, privateKeyB, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signerB, err := ssh.NewSignerFromKey(privateKeyB)
	if err != nil {
		t.Fatal(err)
	}
	hostTCP := &e2ePodSSHHostTCPRegistrar{}
	manager, err := clientpodssh.New(
		remoteClient,
		e2ePodSSHSessionSource{profileID: serverProfile.ID, session: remoteSession},
		clientpodssh.Config{
			ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerA)}, HostTCPRegistrar: hostTCP,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	managerStopped := false
	t.Cleanup(func() {
		if !managerStopped {
			_ = manager.Shutdown()
		}
	})
	endpoint, err := manager.Start(ctx, serverProfile, remoteSession, clientpodssh.Request{
		ProfileID: serverProfile.ID, Namespace: harness.EchoNamespace, Pod: pod.Name,
		Container: "echo", PodIP: pod.Status.PodIP, Ready: true, Containers: containers,
	})
	if err != nil {
		t.Fatalf("start local Pod SSH endpoint: %v", err)
	}

	otherUser, err := dialInterceptedPodSSH(hostTCP, endpoint.Address, podSSHClientConfig(signerB))
	if err == nil {
		_ = otherUser.Close()
		t.Fatal("a different local user's SSH identity accessed the Pod SSH endpoint")
	}
	owner, err := dialInterceptedPodSSH(hostTCP, endpoint.Address, podSSHClientConfig(signerA))
	if err != nil {
		t.Fatalf("authenticate owner to local Pod SSH endpoint: %v", err)
	}
	sshSession, err := owner.NewSession()
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	output, err := sshSession.CombinedOutput("printf 'pod-ssh-through-gateway'")
	if err != nil || !strings.Contains(string(output), "pod-ssh-through-gateway") {
		_ = owner.Close()
		t.Fatalf("real Pod SSH command: output=%q err=%v", output, err)
	}
	sftpClient, err := sftp.NewClient(owner)
	if err != nil {
		_ = owner.Close()
		t.Fatalf("start SFTP through intercepted Pod IP: %v", err)
	}

	// Exercise file and recursive directory transfers through the same native
	// Pod IP interception used by SSH instead of relying on an external client.
	fileContents := []byte("KubeLoop Pod SSH file transfer\n")
	remoteFile := "/tmp/kubeloop-pod-ssh-" + uuid.NewString() + ".txt"
	writeSFTPFile(t, sftpClient, remoteFile, fileContents)
	if uploaded := runInterceptedPodSSHCommand(t, hostTCP, endpoint.Address, signerA, "cat "+remoteFile); !bytes.Equal(uploaded, fileContents) {
		t.Fatalf("Pod SSH uploaded file=%q", uploaded)
	}
	downloaded, err := readSFTPFile(sftpClient, remoteFile)
	if err != nil || !bytes.Equal(downloaded, fileContents) {
		t.Fatalf("Pod SSH downloaded file=%q err=%v", downloaded, err)
	}

	directoryFiles := map[string][]byte{
		"root.txt":         []byte("root through Pod SSH\n"),
		"nested/child.txt": []byte("nested through Pod SSH\n"),
		"nested/empty.bin": {},
	}
	remoteDirectory := "/tmp/kubeloop-pod-ssh-dir-" + uuid.NewString()
	if err := sftpClient.MkdirAll(remoteDirectory + "/nested"); err != nil {
		t.Fatalf("create remote nested directory: %v", err)
	}
	if err := sftpClient.MkdirAll(remoteDirectory + "/empty"); err != nil {
		t.Fatalf("create remote empty directory: %v", err)
	}
	for relative, contents := range directoryFiles {
		writeSFTPFile(t, sftpClient, remoteDirectory+"/"+relative, contents)
	}
	for relative, want := range directoryFiles {
		got, readErr := readSFTPFile(sftpClient, remoteDirectory+"/"+relative)
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("Pod SSH downloaded directory file %s=%q err=%v", relative, got, readErr)
		}
	}
	emptyInfo, err := sftpClient.Stat(remoteDirectory + "/empty")
	if err != nil || !emptyInfo.IsDir() {
		t.Fatalf("Pod SSH remote empty directory=%#v err=%v", emptyInfo, err)
	}
	_ = runInterceptedPodSSHCommand(t, hostTCP, endpoint.Address, signerA, "rm -rf "+remoteFile+" "+remoteDirectory)
	if err := sftpClient.Close(); err != nil {
		t.Fatalf("close intercepted Pod SFTP client: %v", err)
	}
	if err := owner.Close(); err != nil {
		t.Fatalf("close intercepted Pod SSH client: %v", err)
	}

	tasks, err := stateStore.Tasks().ListBySession(ctx, sessionID, 10)
	if err != nil || len(tasks) < 3 {
		t.Fatalf("stored Pod SSH exec Tasks=%#v err=%v", tasks, err)
	}
	for _, task := range tasks {
		if task.Type != execapi.TaskType {
			t.Fatalf("stored Pod SSH Task has type %q: %#v", task.Type, task)
		}
		completed := waitForExecTaskState(t, ctx, stateStore, task.ID, "stopped")
		if len(completed.Result) == 0 {
			t.Fatalf("completed Pod SSH exec Task has no result: %#v", completed)
		}
	}
	if err := manager.Stop(serverProfile.ID, endpoint.ID); err != nil {
		t.Fatalf("stop local Pod SSH endpoint: %v", err)
	}
	managerStopped = true
	if err := manager.Shutdown(); err != nil {
		t.Fatal(err)
	}
	if connection, err := dialInterceptedPodSSH(hostTCP, endpoint.Address, podSSHClientConfig(signerA)); err == nil {
		_ = connection.Close()
		t.Fatal("stopped Pod SSH endpoint still accepted native PodIP traffic")
	}
}

func dialInterceptedPodSSH(
	registrar *e2ePodSSHHostTCPRegistrar,
	address string,
	config *ssh.ClientConfig,
) (*ssh.Client, error) {
	host, rawPort, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.ParseUint(rawPort, 10, 16)
	if err != nil {
		return nil, err
	}
	connection, err := registrar.dial(host, uint16(port))
	if err != nil {
		return nil, err
	}
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, config)
	if err != nil {
		_ = connection.Close()
		return nil, err
	}
	return ssh.NewClient(clientConnection, channels, requests), nil
}

func writeSFTPFile(t *testing.T, client *sftp.Client, path string, contents []byte) {
	t.Helper()
	file, err := client.Create(path)
	if err != nil {
		t.Fatalf("create remote file %s: %v", path, err)
	}
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		t.Fatalf("write remote file %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close remote file %s: %v", path, err)
	}
}

func readSFTPFile(client *sftp.Client, path string) ([]byte, error) {
	file, err := client.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(file)
}

func runInterceptedPodSSHCommand(
	t *testing.T,
	registrar *e2ePodSSHHostTCPRegistrar,
	address string,
	signer ssh.Signer,
	command string,
) []byte {
	t.Helper()
	client, err := dialInterceptedPodSSH(registrar, address, podSSHClientConfig(signer))
	if err != nil {
		t.Fatalf("dial Pod SSH endpoint: %v", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	output, err := session.CombinedOutput(command)
	if err != nil {
		t.Fatalf("run Pod SSH command %q: %v (output=%q)", command, err, output)
	}
	return output
}

func podSSHClientConfig(signer ssh.Signer) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: "echo", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second,
	}
}
