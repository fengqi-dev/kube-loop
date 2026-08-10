//go:build e2e

package v2dataplane

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/credentials"
	clientpodssh "github.com/fengqi-dev/kube-loop/internal/clientv2/podssh"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/profile"
	"github.com/fengqi-dev/kube-loop/internal/clientv2/remote"
	"github.com/fengqi-dev/kube-loop/internal/controller"
	"github.com/fengqi-dev/kube-loop/internal/controller/execapi"
	controllerkubernetes "github.com/fengqi-dev/kube-loop/internal/controller/kubernetes"
	"github.com/fengqi-dev/kube-loop/internal/controller/sessionapi"
	"github.com/fengqi-dev/kube-loop/internal/controller/storage"
	"github.com/fengqi-dev/kube-loop/internal/protocol/networkspec"
	localpodssh "github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type e2ePodSSHSessionSource struct {
	profileID string
	session   remote.Session
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
	deviceID := "v2-e2e-pod-ssh-device"
	if _, err := stateStore.Principals().Upsert(ctx, storage.Principal{
		ID: principalID, Provider: "v2-e2e", ExternalID: "pod-ssh", CreatedAt: now, UpdatedAt: now,
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
	serverProfile := profile.Profile{ID: "v2-e2e-pod-ssh", BaseURL: "http://" + running.Address()}
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
	identityPath := writePodSSHIdentity(t, privateKeyA)
	_, privateKeyB, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	signerB, err := ssh.NewSignerFromKey(privateKeyB)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := clientpodssh.New(
		remoteClient,
		e2ePodSSHSessionSource{profileID: serverProfile.ID, session: remoteSession},
		clientpodssh.Config{ServerOptions: []localpodssh.Option{localpodssh.WithSigner(signerA)}},
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

	otherUser, err := ssh.Dial("tcp", endpoint.Address, podSSHClientConfig(signerB))
	if err == nil {
		_ = otherUser.Close()
		t.Fatal("a different local user's SSH identity accessed the Pod SSH endpoint")
	}
	owner, err := ssh.Dial("tcp", endpoint.Address, podSSHClientConfig(signerA))
	if err != nil {
		t.Fatalf("authenticate owner to local Pod SSH endpoint: %v", err)
	}
	sshSession, err := owner.NewSession()
	if err != nil {
		_ = owner.Close()
		t.Fatal(err)
	}
	output, err := sshSession.CombinedOutput("printf 'pod-ssh-through-gateway'")
	_ = owner.Close()
	if err != nil || !strings.Contains(string(output), "pod-ssh-through-gateway") {
		t.Fatalf("real Pod SSH command: output=%q err=%v", output, err)
	}

	// OpenSSH scp uses the SFTP subsystem by default. Exercise both file and
	// recursive directory transfers through the local SSH endpoint so V2 proves
	// the same bidirectional behavior as the former in-process Pod SSH path.
	fileContents := []byte("KubeLoop V2 Pod SSH file transfer\n")
	localFile := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(localFile, fileContents, 0o600); err != nil {
		t.Fatal(err)
	}
	remoteFile := "/tmp/kubeloop-v2-pod-ssh-" + uuid.NewString() + ".txt"
	runEndpointSCP(t, endpoint.Address, identityPath, false, localFile, podSSHRemote(endpoint.Address, remoteFile))
	if uploaded := runEndpointSSHCommand(t, endpoint.Address, signerA, "cat "+remoteFile); !bytes.Equal(uploaded, fileContents) {
		t.Fatalf("Pod SSH uploaded file=%q", uploaded)
	}
	localDownload := filepath.Join(t.TempDir(), "download.txt")
	runEndpointSCP(t, endpoint.Address, identityPath, false, podSSHRemote(endpoint.Address, remoteFile), localDownload)
	downloaded, err := os.ReadFile(localDownload)
	if err != nil || !bytes.Equal(downloaded, fileContents) {
		t.Fatalf("Pod SSH downloaded file=%q err=%v", downloaded, err)
	}

	localDirectory := filepath.Join(t.TempDir(), "upload-tree")
	if err := os.MkdirAll(filepath.Join(localDirectory, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(localDirectory, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	directoryFiles := map[string][]byte{
		"root.txt":         []byte("root through V2 Pod SSH\n"),
		"nested/child.txt": []byte("nested through V2 Pod SSH\n"),
		"nested/empty.bin": {},
	}
	for relative, contents := range directoryFiles {
		if err := os.WriteFile(filepath.Join(localDirectory, filepath.FromSlash(relative)), contents, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	remoteDirectory := "/tmp/kubeloop-v2-pod-ssh-dir-" + uuid.NewString()
	runEndpointSCP(t, endpoint.Address, identityPath, true, localDirectory, podSSHRemote(endpoint.Address, remoteDirectory))
	for relative, want := range directoryFiles {
		got := runEndpointSSHCommand(t, endpoint.Address, signerA, "cat "+remoteDirectory+"/"+relative)
		if !bytes.Equal(got, want) {
			t.Fatalf("Pod SSH uploaded directory file %s=%q", relative, got)
		}
	}
	if got := runEndpointSSHCommand(
		t, endpoint.Address, signerA,
		"if [ -d "+remoteDirectory+"/empty ]; then printf present; else printf absent; fi",
	); string(got) != "present" {
		t.Fatalf("Pod SSH uploaded empty directory=%q", got)
	}
	localDirectoryDownload := filepath.Join(t.TempDir(), "download-tree")
	runEndpointSCP(t, endpoint.Address, identityPath, true, podSSHRemote(endpoint.Address, remoteDirectory), localDirectoryDownload)
	for relative, want := range directoryFiles {
		got, readErr := os.ReadFile(filepath.Join(localDirectoryDownload, filepath.FromSlash(relative)))
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("Pod SSH downloaded directory file %s=%q err=%v", relative, got, readErr)
		}
	}
	emptyInfo, err := os.Stat(filepath.Join(localDirectoryDownload, "empty"))
	if err != nil || !emptyInfo.IsDir() {
		t.Fatalf("Pod SSH downloaded empty directory=%#v err=%v", emptyInfo, err)
	}
	_ = runEndpointSSHCommand(t, endpoint.Address, signerA, "rm -rf "+remoteFile+" "+remoteDirectory)

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
	rebound, err := net.Listen("tcp", endpoint.Address)
	if err != nil {
		t.Fatalf("Pod SSH loopback address was not released: %v", err)
	}
	_ = rebound.Close()
}

func writePodSSHIdentity(t *testing.T, privateKey ed25519.PrivateKey) string {
	t.Helper()
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop V2 Pod SSH E2E")
	if err != nil {
		t.Fatal(err)
	}
	filename := filepath.Join(t.TempDir(), "id_ed25519")
	if err := os.WriteFile(filename, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	return filename
}

func runEndpointSSHCommand(t *testing.T, address string, signer ssh.Signer, command string) []byte {
	t.Helper()
	client, err := ssh.Dial("tcp", address, podSSHClientConfig(signer))
	if err != nil {
		t.Fatalf("dial V2 Pod SSH endpoint: %v", err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	output, err := session.CombinedOutput(command)
	if err != nil {
		t.Fatalf("run V2 Pod SSH command %q: %v (output=%q)", command, err, output)
	}
	return output
}

func runEndpointSCP(t *testing.T, address, identityPath string, recursive bool, source, destination string) {
	t.Helper()
	binary, err := exec.LookPath("scp")
	if err != nil {
		t.Fatalf("scp is required for V2 Pod SSH E2E: %v", err)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"-q", "-P", port, "-F", os.DevNull, "-i", identityPath,
		"-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + os.DevNull, "-o", "ConnectTimeout=10", "-o", "LogLevel=ERROR",
	}
	if recursive {
		args = append(args, "-r")
	}
	args = append(args, source, destination)
	output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("scp %q -> %q: %v (output=%s)", source, destination, err, output)
	}
}

func podSSHRemote(address, remotePath string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "echo@127.0.0.1:" + remotePath
	}
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("echo@%s:%s", host, remotePath)
}

func podSSHClientConfig(signer ssh.Signer) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User: "echo", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 2 * time.Second,
	}
}
