package podssh

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"
)

func (s *Server) signer() (ssh.Signer, error) {
	s.signerOnce.Do(func() {
		if s.hostSigner != nil {
			return
		}
		path := s.hostKeyPath
		if path == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				s.signerErr = fmt.Errorf("find home directory for SSH host key: %w", err)
				return
			}
			path = filepath.Join(home, ".kubeloop", "ssh_host_ed25519")
		}
		s.hostSigner, s.signerErr = loadOrCreateSigner(path)
	})
	return s.hostSigner, s.signerErr
}

func (s *Server) authorizedClientKeys() ([]ssh.PublicKey, error) {
	s.authOnce.Do(func() {
		if len(s.clientKeys) > 0 {
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			s.authErr = fmt.Errorf("find home directory for Pod SSH identity: %w", err)
			return
		}
		s.clientKeys, s.clientIdentityPath, s.authErr = loadOrCreateUserSSHKeys(home)
	})
	if s.authErr != nil {
		return nil, s.authErr
	}
	if len(s.clientKeys) == 0 {
		return nil, errors.New("Pod SSH client identity is unavailable")
	}
	return append([]ssh.PublicKey{}, s.clientKeys...), nil
}

func loadOrCreateUserSSHKeys(home string) ([]ssh.PublicKey, string, error) {
	sshDir := filepath.Join(home, ".ssh")
	names := []string{"id_ed25519", "id_ecdsa", "id_rsa"}
	keys := make([]ssh.PublicKey, 0, len(names))
	identityPath := ""
	var firstErr error
	ed25519Occupied := false
	for _, name := range names {
		privatePath := filepath.Join(sshDir, name)
		publicPath := privatePath + ".pub"
		if name == "id_ed25519" {
			_, privateErr := os.Stat(privatePath)
			_, publicErr := os.Stat(publicPath)
			ed25519Occupied = privateErr == nil || publicErr == nil
		}
		var publicKey ssh.PublicKey
		if content, err := os.ReadFile(publicPath); err == nil {
			key, _, _, _, parseErr := ssh.ParseAuthorizedKey(content)
			if parseErr == nil {
				publicKey = key
			} else if firstErr == nil {
				firstErr = fmt.Errorf("parse user SSH public key %s: %w", publicPath, parseErr)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("read user SSH public key %s: %w", publicPath, err)
		}
		if content, err := os.ReadFile(privatePath); err == nil {
			signer, parseErr := ssh.ParsePrivateKey(content)
			if parseErr == nil {
				keys = append(keys, signer.PublicKey())
				if identityPath == "" {
					identityPath = privatePath
				}
				continue
			}
			if publicKey != nil {
				keys = append(keys, publicKey)
				if identityPath == "" {
					identityPath = privatePath
				}
				continue
			}
			if firstErr == nil {
				firstErr = fmt.Errorf(
					"parse user SSH private key %s (create its .pub file if it is encrypted): %w",
					privatePath, parseErr,
				)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("read user SSH private key %s: %w", privatePath, err)
		}
		if publicKey != nil {
			keys = append(keys, publicKey)
		}
	}
	if len(keys) > 0 {
		return keys, identityPath, nil
	}
	if ed25519Occupied {
		if firstErr != nil {
			return nil, "", firstErr
		}
		return nil, "", errors.New("~/.ssh/id_ed25519 exists but no usable public key was found")
	}
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		return nil, "", fmt.Errorf("create user SSH directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("generate user SSH identity: %w", err)
	}
	privatePath := filepath.Join(sshDir, "id_ed25519")
	if err := writeNewOpenSSHPrivateKey(privatePath, privateKey); err != nil {
		return nil, "", fmt.Errorf("write user SSH identity: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		_ = os.Remove(privatePath)
		return nil, "", fmt.Errorf("create user SSH signer: %w", err)
	}
	publicPath := privatePath + ".pub"
	if err := writeNewFile(publicPath, ssh.MarshalAuthorizedKey(signer.PublicKey()), 0o644); err != nil {
		_ = os.Remove(privatePath)
		return nil, "", fmt.Errorf("write user SSH public key: %w", err)
	}
	return []ssh.PublicKey{signer.PublicKey()}, privatePath, nil
}

func loadOrCreateSigner(path string) (ssh.Signer, error) {
	if content, err := os.ReadFile(path); err == nil {
		privateKey, parseErr := ssh.ParseRawPrivateKey(content)
		if parseErr != nil {
			return nil, fmt.Errorf("parse Pod SSH key: %w", parseErr)
		}
		signer, parseErr := ssh.NewSignerFromKey(privateKey)
		if parseErr != nil {
			return nil, fmt.Errorf("create Pod SSH signer: %w", parseErr)
		}
		block, _ := pem.Decode(content)
		if block == nil || block.Type != "OPENSSH PRIVATE KEY" {
			if err := writeOpenSSHPrivateKey(path, privateKey); err != nil {
				return nil, fmt.Errorf("migrate Pod SSH key to OpenSSH format: %w", err)
			}
		} else if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure Pod SSH key permissions: %w", err)
		}
		return signer, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read Pod SSH key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create Pod SSH key directory: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate Pod SSH key: %w", err)
	}
	if err := writeOpenSSHPrivateKey(path, privateKey); err != nil {
		return nil, fmt.Errorf("write Pod SSH key: %w", err)
	}
	return ssh.NewSignerFromKey(privateKey)
}

func writeNewOpenSSHPrivateKey(path string, privateKey any) error {
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop Pod SSH")
	if err != nil {
		return fmt.Errorf("encode OpenSSH private key: %w", err)
	}
	return writeNewFile(path, pem.EncodeToMemory(block), 0o600)
}

func writeNewFile(path string, content []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	ok = true
	return nil
}

func writeOpenSSHPrivateKey(path string, privateKey any) error {
	block, err := ssh.MarshalPrivateKey(privateKey, "KubeLoop Pod SSH")
	if err != nil {
		return fmt.Errorf("encode OpenSSH private key: %w", err)
	}
	content := pem.EncodeToMemory(block)
	temp, err := os.CreateTemp(filepath.Dir(path), ".pod_ssh_key-*")
	if err != nil {
		return fmt.Errorf("create temporary key: %w", err)
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("install key: %w", err)
	}
	return nil
}
