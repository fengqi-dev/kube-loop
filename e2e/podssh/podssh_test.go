//go:build e2e

package podssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/e2e/harness"
	podsshserver "github.com/fengqi-dev/kube-loop/internal/podssh"
	"github.com/fengqi-dev/kube-loop/internal/session"
	"golang.org/x/crypto/ssh"
)

func TestMain(m *testing.M) { harness.RunMain(m) }

func TestPodSSHSelectsContainerFromLoginName(t *testing.T) {
	harness.RequireE2E(t)
	ctx, cancel := harness.TestContext(t, 2*time.Minute)
	defer cancel()

	provider := harness.NewProvider(t)
	client := harness.KubeClient(t, provider)
	if err := harness.EnsureEchoWorkload(ctx, client); err != nil {
		t.Fatal(err)
	}
	podName, podIP := harness.EchoPodIP(t, ctx, client)
	clusterIP := harness.EchoServiceIP(t, ctx, client)
	identity := newIdentity(t)

	live := harness.ConnectSession(t, ctx, session.Request{
		Context: harness.KubeContext(), Namespace: harness.EchoNamespace,
	}, func(manager *session.Manager) {
		session.WithPodSSHOptions(
			podsshserver.WithSigner(identity.signer),
			podsshserver.WithClientIdentityPath(identity.path),
		)(manager)
	})
	harness.RequireRoutedViaKubeLoop(t, podIP, clusterIP)

	info := waitPodSSHEndpoint(t, live.Manager, podName)
	if want := "ssh -i '" + identity.path + "' echo@" + podIP; info.Command != want {
		t.Fatalf("copy command = %q, want %q", info.Command, want)
	}

	for _, container := range []string{"echo", "sidecar"} {
		t.Run(container, func(t *testing.T) {
			output := runSSHCommand(
				t,
				podIP,
				container,
				identity.signer,
				`printf %s "$KUBELOOP_E2E_CONTAINER"`,
			)
			if string(output) != container {
				t.Fatalf("SSH login %q executed in %q", container, output)
			}
		})
	}

	t.Run("scp-bidirectional", func(t *testing.T) {
		testSCPBidirectional(t, podIP, identity)
	})
	t.Run("scp-directory-bidirectional", func(t *testing.T) {
		testSCPDirectoryBidirectional(t, podIP, identity)
	})

	clientConfig := sshClientConfig("missing", identity.signer)
	connection, err := ssh.Dial("tcp", net.JoinHostPort(podIP, "22"), clientConfig)
	if err == nil {
		_ = connection.Close()
		t.Fatal("unknown container login unexpectedly authenticated")
	}
}

func testSCPBidirectional(t *testing.T, podIP string, identity testIdentity) {
	t.Helper()
	content := []byte("KubeLoop SCP local-to-Pod and Pod-to-local\n")
	localSource := filepath.Join(t.TempDir(), "upload.txt")
	if err := os.WriteFile(localSource, content, 0o600); err != nil {
		t.Fatal(err)
	}
	remotePath := fmt.Sprintf("/tmp/kubeloop-e2e-scp-%d.txt", time.Now().UnixNano())
	remote := scpRemote("sidecar", podIP, remotePath)

	runSCP(t, identity, false, localSource, remote)
	inContainer := runSSHCommand(
		t,
		podIP,
		"sidecar",
		identity.signer,
		"cat "+remotePath,
	)
	if !bytes.Equal(inContainer, content) {
		t.Fatalf("uploaded SCP content = %q, want %q", inContainer, content)
	}
	echoContainer := runSSHCommand(
		t,
		podIP,
		"echo",
		identity.signer,
		"if [ -e "+remotePath+" ]; then printf present; else printf absent; fi",
	)
	if string(echoContainer) != "absent" {
		t.Fatalf("SCP upload leaked into echo container: %q", echoContainer)
	}

	localDownload := filepath.Join(t.TempDir(), "download.txt")
	runSCP(t, identity, false, remote, localDownload)
	downloaded, err := os.ReadFile(localDownload)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded, content) {
		t.Fatalf("downloaded SCP content = %q, want %q", downloaded, content)
	}
	_ = runSSHCommand(t, podIP, "sidecar", identity.signer, "rm -f "+remotePath)
}

func testSCPDirectoryBidirectional(t *testing.T, podIP string, identity testIdentity) {
	t.Helper()
	localSource := filepath.Join(t.TempDir(), "upload-tree")
	nested := filepath.Join(localSource, "nested")
	empty := filepath.Join(localSource, "empty")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"root.txt":              []byte("root-level SCP directory file\n"),
		"nested/child.txt":      []byte("nested SCP directory file\n"),
		"nested/zero-bytes.bin": {},
	}
	for relative, content := range files {
		if err := os.WriteFile(filepath.Join(localSource, filepath.FromSlash(relative)), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	remotePath := fmt.Sprintf("/tmp/kubeloop-e2e-scp-dir-%d", time.Now().UnixNano())
	remote := scpRemote("sidecar", podIP, remotePath)
	runSCP(t, identity, true, localSource, remote)

	for relative, want := range files {
		got := runSSHCommand(
			t,
			podIP,
			"sidecar",
			identity.signer,
			"cat "+remotePath+"/"+relative,
		)
		if !bytes.Equal(got, want) {
			t.Fatalf("uploaded directory file %s = %q, want %q", relative, got, want)
		}
	}
	emptyResult := runSSHCommand(
		t,
		podIP,
		"sidecar",
		identity.signer,
		"if [ -d "+remotePath+"/empty ]; then printf present; else printf absent; fi",
	)
	if string(emptyResult) != "present" {
		t.Fatalf("empty uploaded directory is missing: %q", emptyResult)
	}
	echoContainer := runSSHCommand(
		t,
		podIP,
		"echo",
		identity.signer,
		"if [ -e "+remotePath+" ]; then printf present; else printf absent; fi",
	)
	if string(echoContainer) != "absent" {
		t.Fatalf("recursive SCP upload leaked into echo container: %q", echoContainer)
	}

	localDownload := filepath.Join(t.TempDir(), "download-tree")
	runSCP(t, identity, true, remote, localDownload)
	for relative, want := range files {
		got, err := os.ReadFile(filepath.Join(localDownload, filepath.FromSlash(relative)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("downloaded directory file %s = %q, want %q", relative, got, want)
		}
	}
	info, err := os.Stat(filepath.Join(localDownload, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	if !info.IsDir() {
		t.Fatal("downloaded empty entry is not a directory")
	}
	_ = runSSHCommand(t, podIP, "sidecar", identity.signer, "rm -rf "+remotePath)
}

func runSCP(
	t *testing.T,
	identity testIdentity,
	recursive bool,
	source,
	destination string,
) {
	t.Helper()
	binary, err := exec.LookPath("scp")
	if err != nil {
		t.Fatalf("scp is required for Pod SSH E2E: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args := []string{
		"-q",
		"-F", os.DevNull,
		"-i", identity.path,
		"-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=" + identity.knownHostsPath,
		"-o", "ConnectTimeout=10",
		"-o", "LogLevel=ERROR",
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

func scpRemote(container, podIP, path string) string {
	host := podIP
	if ip := net.ParseIP(podIP); ip != nil && ip.To4() == nil {
		host = "[" + podIP + "]"
	}
	return fmt.Sprintf("%s@%s:%s", container, host, path)
}

func waitPodSSHEndpoint(
	t *testing.T,
	manager *session.Manager,
	podName string,
) podsshserver.Info {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		for _, info := range manager.ListPodSSH() {
			if info.Namespace == harness.EchoNamespace && info.Pod == podName {
				return info
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("default Pod SSH endpoint for %s/%s was not published", harness.EchoNamespace, podName)
	return podsshserver.Info{}
}

func runSSHCommand(
	t *testing.T,
	podIP string,
	container string,
	signer ssh.Signer,
	command string,
) []byte {
	t.Helper()
	client, err := ssh.Dial(
		"tcp",
		net.JoinHostPort(podIP, "22"),
		sshClientConfig(container, signer),
	)
	if err != nil {
		t.Fatalf("SSH %s@%s: %v", container, podIP, err)
	}
	defer client.Close()
	remote, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	output, err := remote.CombinedOutput(command)
	if err != nil {
		t.Fatalf("SSH command in %s: %v (output=%q)", container, err, output)
	}
	return output
}

func sshClientConfig(container string, signer ssh.Signer) *ssh.ClientConfig {
	return &ssh.ClientConfig{
		User:            container,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}
}

type testIdentity struct {
	signer         ssh.Signer
	path           string
	knownHostsPath string
}

func newIdentity(t *testing.T) testIdentity {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop Pod SSH E2E")
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	path := filepath.Join(directory, "id_ed25519")
	if err := os.WriteFile(path, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	knownHostsPath := filepath.Join(directory, "known_hosts")
	if err := os.WriteFile(knownHostsPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return testIdentity{
		signer: signer, path: path, knownHostsPath: knownHostsPath,
	}
}
