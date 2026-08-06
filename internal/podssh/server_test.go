package podssh

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kballard/go-shellquote"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type fakeExecutor struct {
	mu       sync.Mutex
	commands [][]string
	targets  []Target
	files    map[string][]byte
}

func (f *fakeExecutor) Exec(
	_ context.Context,
	target Target,
	command []string,
	streams Streams,
) error {
	f.mu.Lock()
	f.commands = append(f.commands, append([]string{}, command...))
	f.targets = append(f.targets, target)
	f.mu.Unlock()
	script := ""
	if len(command) >= 3 {
		script = command[2]
	}
	switch {
	case strings.Contains(script, "tar xf - -C "+shellquote.Join("/tmp")):
		archive := tar.NewReader(streams.Stdin)
		header, err := archive.Next()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(archive)
		if err != nil {
			return err
		}
		for {
			_, err = archive.Next()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return err
			}
			if _, err = io.Copy(io.Discard, archive); err != nil {
				return err
			}
		}
		f.mu.Lock()
		if f.files == nil {
			f.files = make(map[string][]byte)
		}
		f.files["/tmp/"+header.Name] = content
		f.mu.Unlock()
		return nil
	case strings.Contains(script, "tar cf - -C "+shellquote.Join("/tmp", "hello.txt")):
		f.mu.Lock()
		content, ok := f.files["/tmp/hello.txt"]
		f.mu.Unlock()
		if !ok {
			// GNU tar emits a 10 KiB padded empty archive even when the named
			// file is missing. The SFTP reader must consume the padding before
			// waiting for Exec to return.
			if _, err := streams.Stdout.Write(make([]byte, 10*1024)); err != nil {
				return err
			}
			return errors.New("pod exec failed")
		}
		archive := tar.NewWriter(streams.Stdout)
		if err := archive.WriteHeader(&tar.Header{
			Name: "hello.txt", Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			return err
		}
		if _, err := archive.Write(content); err != nil {
			return err
		}
		return archive.Close()
	default:
		if streams.Stdout != nil {
			_, _ = io.WriteString(streams.Stdout, "exec-ok\n")
		}
		return nil
	}
}

func testSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func TestServerExecOverPodIP(t *testing.T) {
	executor := &fakeExecutor{}
	signer := testSigner(t)
	server := NewServer(
		executor,
		WithSigner(signer),
		WithClientIdentityPath("/tmp/id_ed25519"),
	)
	target := Target{
		Context: "dev", Namespace: "default", Pod: "api-123", Container: "api", IP: "10.244.1.7",
		Containers: []string{"api", "sidecar"},
	}
	info, err := server.Enable(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Command != "ssh -i "+shellquote.Join("/tmp/id_ed25519")+" api@10.244.1.7" {
		t.Fatalf("command=%q", info.Command)
	}
	serve, claimed := server.HostTCP(target.IP, DefaultPort)
	if !claimed {
		t.Fatal("Pod IP:22 was not claimed")
	}
	if _, claimed := server.HostTCP(target.IP, 80); claimed {
		t.Fatal("non-SSH Pod port was claimed")
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			serve(connection)
		}
	}()
	client, err := ssh.Dial("tcp", listener.Addr().String(), &ssh.ClientConfig{
		User:            "sidecar",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	output, err := session.CombinedOutput("printf hello")
	if err != nil {
		t.Fatal(err)
	}
	if string(output) != "exec-ok\n" {
		t.Fatalf("output=%q", output)
	}

	fileClient, err := sftp.NewClient(client)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := fileClient.Create("/tmp/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := remote.Write([]byte("copied through sftp")); err != nil {
		t.Fatal(err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	remote, err = fileClient.Open("/tmp/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	copied, err := io.ReadAll(remote)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fileClient.Close(); err != nil {
		t.Fatal(err)
	}
	if string(copied) != "copied through sftp" {
		t.Fatalf("sftp content=%q", copied)
	}

	executor.mu.Lock()
	defer executor.mu.Unlock()
	first := executor.commands[0]
	if got := strings.Join(first, "\x00"); got != "/bin/sh\x00-c\x00printf hello" {
		t.Fatalf("command=%q", first)
	}
	if executor.targets[0].Container != "sidecar" {
		t.Fatalf("container=%q", executor.targets[0].Container)
	}
}

func TestTargetForLoginRejectsUnknownContainer(t *testing.T) {
	target := Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api",
		Containers: []string{"api", "sidecar"}, IP: "10.0.0.2",
	}
	selected, ok := targetForLogin(target, "sidecar")
	if !ok || selected.Container != "sidecar" {
		t.Fatalf("selected=%#v ok=%v", selected, ok)
	}
	if _, ok := targetForLogin(target, "missing"); ok {
		t.Fatal("unknown container login was accepted")
	}
}

func TestSFTPHandlerCopiesFilesWithTarStreams(t *testing.T) {
	executor := &fakeExecutor{files: make(map[string][]byte)}
	handler := newSFTPHandler(executor, Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api", IP: "10.0.0.2",
	})
	put := sftp.NewRequest("Put", "/tmp/hello.txt")
	put.Flags = 2 | 8 | 16 // SSH_FXF_WRITE | SSH_FXF_CREAT | SSH_FXF_TRUNC
	writer, err := handler.Filewrite(put)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteAt([]byte("hello from sftp"), 0); err != nil {
		t.Fatal(err)
	}
	closer, ok := writer.(io.Closer)
	if !ok {
		t.Fatal("upload writer is not closable")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	get := sftp.NewRequest("Get", "/tmp/hello.txt")
	reader, err := handler.Fileread(get)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]byte, len("hello from sftp"))
	if _, err := reader.ReadAt(buffer, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	if !bytes.Equal(buffer, []byte("hello from sftp")) {
		t.Fatalf("content=%q", buffer)
	}
	if closer, ok := reader.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSFTPHandlerCreatesMissingFileWithoutTruncateFlag(t *testing.T) {
	executor := &fakeExecutor{files: make(map[string][]byte)}
	handler := newSFTPHandler(executor, Target{
		Context: "dev", Namespace: "default", Pod: "api", Container: "api", IP: "10.0.0.2",
	})
	put := sftp.NewRequest("Put", "/tmp/hello.txt")
	put.Flags = 2 | 8 // SSH_FXF_WRITE | SSH_FXF_CREAT, as sent by OpenSSH scp.
	type openResult struct {
		writer io.WriterAt
		err    error
	}
	opened := make(chan openResult, 1)
	go func() {
		writer, err := handler.Filewrite(put)
		opened <- openResult{writer: writer, err: err}
	}()
	var writer io.WriterAt
	select {
	case result := <-opened:
		if result.err != nil {
			t.Fatal(result.err)
		}
		writer = result.writer
	case <-time.After(2 * time.Second):
		t.Fatal("SFTP open deadlocked on GNU tar end padding")
	}
	if _, err := writer.WriteAt([]byte("created by scp"), 0); err != nil {
		t.Fatal(err)
	}
	closer, ok := writer.(io.Closer)
	if !ok {
		t.Fatal("upload writer is not closable")
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
	if got := string(executor.files["/tmp/hello.txt"]); got != "created by scp" {
		t.Fatalf("uploaded content=%q", got)
	}
}

func TestReconcileMovesEnabledEndpointToReplacementPodIP(t *testing.T) {
	server := NewServer(&fakeExecutor{}, WithSigner(testSigner(t)))
	if err := server.Reconcile([]PodRef{{
		Context: "dev", Namespace: "default", Pod: "api",
		IP: "10.0.0.2", Containers: []string{"api"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, claimed := server.HostTCP("10.0.0.2", 22); !claimed {
		t.Fatal("default Pod SSH endpoint was not enabled")
	}
	if err := server.Reconcile([]PodRef{{
		Context: "dev", Namespace: "default", Pod: "api",
		IP: "10.0.0.9", Containers: []string{"api"},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, claimed := server.HostTCP("10.0.0.2", 22); claimed {
		t.Fatal("old Pod IP remained enabled")
	}
	if _, claimed := server.HostTCP("10.0.0.9", 22); !claimed {
		t.Fatalf("replacement Pod IP was not enabled: %#v", server.List())
	}
}

func TestLoadOrCreateSignerMigratesPKCS8ForOpenSSH(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "pod_ssh_ed25519")
	legacy := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: encoded})
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}

	signer, err := loadOrCreateSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		t.Fatal("migrated private key is not PEM encoded")
	}
	if block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatalf("private key type=%q", block.Type)
	}
	expected, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(signer.PublicKey().Marshal(), expected.PublicKey().Marshal()) {
		t.Fatal("migration changed the public key")
	}
	assertOpenSSHCanReadPrivateKey(t, path, signer)
}

func TestLoadOrCreateSignerCreatesOpenSSHPrivateKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "pod_ssh_ed25519")
	signer, err := loadOrCreateSigner(path)
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(content)
	if block == nil {
		t.Fatal("generated private key is not PEM encoded")
	}
	if block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatalf("private key type=%q", block.Type)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("private key mode=%o", info.Mode().Perm())
	}
	assertOpenSSHCanReadPrivateKey(t, path, signer)
}

func TestLoadOrCreateUserSSHKeysPrefersExistingIdentity(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(sshDir, "id_ed25519")
	if err := writeNewOpenSSHPrivateKey(privatePath, privateKey); err != nil {
		t.Fatal(err)
	}
	privateBefore, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	publicContent := ssh.MarshalAuthorizedKey(signer.PublicKey())
	if err := os.WriteFile(privatePath+".pub", publicContent, 0o644); err != nil {
		t.Fatal(err)
	}

	keys, identityPath, err := loadOrCreateUserSSHKeys(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !bytes.Equal(keys[0].Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal("existing user SSH key was not selected")
	}
	if identityPath != privatePath {
		t.Fatalf("identity path=%q, want %q", identityPath, privatePath)
	}
	privateAfter, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(privateBefore, privateAfter) {
		t.Fatal("existing user SSH private key was overwritten")
	}
}

func TestLoadOrCreateUserSSHKeysGeneratesDefaultIdentity(t *testing.T) {
	home := t.TempDir()
	keys, identityPath, err := loadOrCreateUserSSHKeys(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 {
		t.Fatalf("keys=%d", len(keys))
	}
	privatePath := filepath.Join(home, ".ssh", "id_ed25519")
	if identityPath != privatePath {
		t.Fatalf("identity path=%q, want %q", identityPath, privatePath)
	}
	content, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(content)
	if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
		t.Fatal("generated default identity is not an OpenSSH private key")
	}
	publicContent, err := os.ReadFile(privatePath + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey(publicContent)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(publicKey.Marshal(), keys[0].Marshal()) {
		t.Fatal("generated public and private identities do not match")
	}
	assertOpenSSHCanReadPrivateKey(t, privatePath, mustSigner(t, content))
}

func mustSigner(t *testing.T, content []byte) ssh.Signer {
	t.Helper()
	signer, err := ssh.ParsePrivateKey(content)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func assertOpenSSHCanReadPrivateKey(t *testing.T, path string, signer ssh.Signer) {
	t.Helper()
	sshKeygen, err := exec.LookPath("ssh-keygen")
	if err != nil {
		t.Log("ssh-keygen is unavailable; skipping system OpenSSH compatibility check")
		return
	}
	output, err := exec.Command(sshKeygen, "-y", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen rejected private key: %v: %s", err, output)
	}
	publicKey, _, _, _, err := ssh.ParseAuthorizedKey(output)
	if err != nil {
		t.Fatalf("parse ssh-keygen public key: %v", err)
	}
	if !bytes.Equal(publicKey.Marshal(), signer.PublicKey().Marshal()) {
		t.Fatal("ssh-keygen returned a different public key")
	}
}
