package credentials

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	serviceName                     = "KubeLoop"
	developmentServiceName          = "KubeLoop Dev"
	credentialMetadataSchemaVersion = 1
)

var ErrNotFound = errors.New("credentials not found")

type Credential struct {
	TokenType        string
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
	DeviceID         string
	IdentityID       string
	UserName         string
}

type Store interface {
	Set(profileID string, credential Credential) error
	Get(profileID string) (Credential, error)
	Delete(profileID string) error
}

type Backend interface {
	Set(service, account, secret string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

type SystemStore struct {
	backend Backend
	random  func([]byte) error
	service string
}

type metadata struct {
	SchemaVersion    int       `json:"schemaVersion"`
	TokenType        string    `json:"tokenType"`
	AccessExpiresAt  time.Time `json:"accessExpiresAt"`
	RefreshExpiresAt time.Time `json:"refreshExpiresAt"`
	DeviceID         string    `json:"deviceId"`
	IdentityID       string    `json:"identityId,omitempty"`
	UserName         string    `json:"userName,omitempty"`
}

type systemBackend struct{}

func (systemBackend) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (systemBackend) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemBackend) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

func NewSystemStore() *SystemStore {
	return NewStore(systemBackend{})
}

func NewSystemStoreForVersion(version string) *SystemStore {
	return newStore(systemBackend{}, keyringServiceForVersion(version))
}

// NewSystemStoreForClient isolates OAuth credentials by client ID while still
// allowing Desktop and TUI to share Server profiles. Refresh tokens are bound
// to the OAuth client that obtained them and must never overwrite each other.
func NewSystemStoreForClient(version, clientID string) *SystemStore {
	return newStoreForClient(systemBackend{}, version, clientID)
}

func NewStore(backend Backend) *SystemStore {
	return newStore(backend, serviceName)
}

func newStore(backend Backend, service string) *SystemStore {
	return &SystemStore{backend: backend, service: service, random: func(value []byte) error {
		_, err := rand.Read(value)
		return err
	}}
}

func newStoreForClient(backend Backend, version, clientID string) *SystemStore {
	return newStore(backend, keyringServiceForClient(version, clientID))
}

func keyringServiceForVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "dev" {
		return developmentServiceName
	}
	return serviceName
}

func keyringServiceForClient(version, clientID string) string {
	clientID = strings.TrimSpace(clientID)
	if clientID == "" {
		return keyringServiceForVersion(version)
	}
	return keyringServiceForVersion(version) + " OAuth " + clientID
}

func (store *SystemStore) Set(profileID string, credential Credential) error {
	prefix, err := accountPrefix(profileID)
	if err != nil {
		return err
	}
	if store.backend == nil || strings.TrimSpace(credential.AccessToken) == "" ||
		strings.TrimSpace(credential.RefreshToken) == "" || strings.TrimSpace(credential.DeviceID) == "" {
		return errors.New("complete credentials are required")
	}
	generationBytes := make([]byte, 16)
	if err := store.random(generationBytes); err != nil {
		return errors.New("generate credential version")
	}
	generation := hex.EncodeToString(generationBytes)
	metadataJSON, err := json.Marshal(metadata{
		SchemaVersion: credentialMetadataSchemaVersion,
		TokenType:     credential.TokenType, AccessExpiresAt: credential.AccessExpiresAt.UTC(),
		RefreshExpiresAt: credential.RefreshExpiresAt.UTC(), DeviceID: credential.DeviceID,
		IdentityID: strings.TrimSpace(credential.IdentityID), UserName: strings.TrimSpace(credential.UserName),
	})
	if err != nil {
		return errors.New("encode credential metadata")
	}
	newAccounts := credentialAccounts(prefix, generation)
	values := []string{credential.AccessToken, credential.RefreshToken, string(metadataJSON)}
	for index, account := range newAccounts {
		if err := store.backend.Set(store.service, account, values[index]); err != nil {
			_ = store.deleteAccounts(newAccounts[:index])
			return fmt.Errorf("store credentials in system keyring: %w", err)
		}
	}
	oldGeneration, _ := store.backend.Get(store.service, prefix+":current")
	if err := store.backend.Set(store.service, prefix+":current", generation); err != nil {
		_ = store.deleteAccounts(newAccounts)
		return fmt.Errorf("activate credentials in system keyring: %w", err)
	}
	if oldGeneration != "" && oldGeneration != generation {
		if err := store.deleteAccounts(credentialAccounts(prefix, oldGeneration)); err != nil {
			return fmt.Errorf("remove superseded credentials from system keyring: %w", err)
		}
	}
	return nil
}

func (store *SystemStore) Get(profileID string) (Credential, error) {
	prefix, err := accountPrefix(profileID)
	if err != nil {
		return Credential{}, err
	}
	if store.backend == nil {
		return Credential{}, errors.New("system keyring is unavailable")
	}
	generation, err := store.backend.Get(store.service, prefix+":current")
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return Credential{}, ErrNotFound
		}
		return Credential{}, fmt.Errorf("read credentials from system keyring: %w", err)
	}
	accounts := credentialAccounts(prefix, generation)
	accessToken, err := store.backend.Get(store.service, accounts[0])
	if err != nil {
		return Credential{}, errors.New("system keyring credentials are incomplete")
	}
	refreshToken, err := store.backend.Get(store.service, accounts[1])
	if err != nil {
		return Credential{}, errors.New("system keyring credentials are incomplete")
	}
	metadataJSON, err := store.backend.Get(store.service, accounts[2])
	if err != nil {
		return Credential{}, errors.New("system keyring credentials are incomplete")
	}
	var details metadata
	decoder := json.NewDecoder(strings.NewReader(metadataJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&details); err != nil || strings.TrimSpace(details.DeviceID) == "" {
		return Credential{}, errors.New("system keyring credential metadata is invalid")
	}
	if details.SchemaVersion != credentialMetadataSchemaVersion {
		return Credential{}, ErrNotFound
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Credential{}, errors.New("system keyring credential metadata is invalid")
	}
	return Credential{
		TokenType: details.TokenType, AccessToken: accessToken, AccessExpiresAt: details.AccessExpiresAt,
		RefreshToken: refreshToken, RefreshExpiresAt: details.RefreshExpiresAt, DeviceID: details.DeviceID,
		IdentityID: strings.TrimSpace(details.IdentityID), UserName: strings.TrimSpace(details.UserName),
	}, nil
}

func (store *SystemStore) Delete(profileID string) error {
	prefix, err := accountPrefix(profileID)
	if err != nil {
		return err
	}
	if store.backend == nil {
		return errors.New("system keyring is unavailable")
	}
	generation, getErr := store.backend.Get(store.service, prefix+":current")
	if getErr != nil && !errors.Is(getErr, keyring.ErrNotFound) {
		return fmt.Errorf("read credentials from system keyring: %w", getErr)
	}
	if generation != "" {
		if err := store.deleteAccounts(credentialAccounts(prefix, generation)); err != nil {
			return fmt.Errorf("delete credentials from system keyring: %w", err)
		}
	}
	if err := store.backend.Delete(
		store.service,
		prefix+":current",
	); err != nil &&
		!errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("delete credentials from system keyring: %w", err)
	}
	return nil
}

func (store *SystemStore) deleteAccounts(accounts []string) error {
	var result error
	for _, account := range accounts {
		if err := store.backend.Delete(store.service, account); err != nil && !errors.Is(err, keyring.ErrNotFound) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func accountPrefix(profileID string) (string, error) {
	profileID = strings.TrimSpace(profileID)
	if profileID == "" {
		return "", errors.New("server Profile ID is required")
	}
	hash := sha256.Sum256([]byte(profileID))
	return hex.EncodeToString(hash[:]), nil
}

func credentialAccounts(prefix, generation string) []string {
	return []string{
		prefix + ":" + generation + ":access",
		prefix + ":" + generation + ":refresh",
		prefix + ":" + generation + ":metadata",
	}
}
