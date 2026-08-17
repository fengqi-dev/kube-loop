package remote

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

type InventoryResource string

const (
	InventoryPods     InventoryResource = "pods"
	InventoryServices InventoryResource = "services"
)

type InventorySnapshot struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Type            string            `json:"type"`
	Resource        InventoryResource `json:"resource"`
	Namespace       string            `json:"namespace"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Sequence        uint64            `json:"sequence"`
	GeneratedAt     time.Time         `json:"generatedAt"`
	Pods            []Pod             `json:"pods,omitempty"`
	Services        []Service         `json:"services,omitempty"`
}

type InventoryWatch struct {
	connection *websocket.Conn
	resource   InventoryResource
	namespace  string
	mu         sync.Mutex
	sequence   uint64
	closeOnce  sync.Once
}

func (client *Client) OpenInventoryWatch(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
	resource InventoryResource,
) (*InventoryWatch, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	if resource != InventoryPods && resource != InventoryServices {
		return nil, errors.New("Inventory Watch resource is invalid")
	}
	baseURL, err := profile.NormalizeBaseURL(serverProfile.BaseURL)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil {
		return nil, errors.New("Server Profile URL is invalid")
	}
	if endpoint.Scheme == "https" {
		endpoint.Scheme = "wss"
	} else {
		endpoint.Scheme = "ws"
	}
	endpoint.Path = "/kubeloop/api/namespaces/" + url.PathEscape(namespace) + "/" + string(resource)
	endpoint.RawQuery = url.Values{"watch": {"true"}}.Encode()
	credential, err := client.usableCredential(ctx, serverProfile, "")
	if err != nil {
		return nil, err
	}
	connection, response, err := client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	if err != nil && response != nil && response.StatusCode == http.StatusUnauthorized {
		credential, refreshErr := client.usableCredential(ctx, serverProfile, credential.AccessToken)
		if refreshErr != nil {
			return nil, refreshErr
		}
		connection, _, err = client.dialWebSocket(ctx, endpoint.String(), credential.AccessToken)
	}
	if err != nil {
		return nil, err
	}
	connection.SetReadLimit(maximumResponseBytes)
	return &InventoryWatch{connection: connection, resource: resource, namespace: namespace}, nil
}

func (watch *InventoryWatch) Next(ctx context.Context) (InventorySnapshot, error) {
	if watch == nil || watch.connection == nil {
		return InventorySnapshot{}, errors.New("Inventory Watch is unavailable")
	}
	watch.mu.Lock()
	defer watch.mu.Unlock()
	messageType, encoded, err := watch.connection.Read(ctx)
	if err != nil {
		return InventorySnapshot{}, err
	}
	if messageType != websocket.MessageText {
		return InventorySnapshot{}, errors.New("Gateway returned a non-text Inventory snapshot")
	}
	var snapshot InventorySnapshot
	if err := json.Unmarshal(encoded, &snapshot); err != nil {
		return InventorySnapshot{}, errors.New("Gateway returned an invalid Inventory snapshot")
	}
	if snapshot.SchemaVersion != 1 || snapshot.Type != "snapshot" || snapshot.Resource != watch.resource ||
		snapshot.Namespace != watch.namespace || snapshot.Sequence <= watch.sequence || snapshot.GeneratedAt.IsZero() {
		return InventorySnapshot{}, errors.New("Gateway returned an invalid Inventory snapshot binding")
	}
	for _, pod := range snapshot.Pods {
		if pod.Namespace != watch.namespace || strings.TrimSpace(pod.Name) == "" {
			return InventorySnapshot{}, errors.New("Gateway returned a cross-namespace Pod snapshot")
		}
	}
	for _, service := range snapshot.Services {
		if service.Namespace != watch.namespace || strings.TrimSpace(service.Name) == "" {
			return InventorySnapshot{}, errors.New("Gateway returned a cross-namespace Service snapshot")
		}
	}
	if snapshot.Pods == nil {
		snapshot.Pods = []Pod{}
	}
	if snapshot.Services == nil {
		snapshot.Services = []Service{}
	}
	watch.sequence = snapshot.Sequence
	return snapshot, nil
}

func (watch *InventoryWatch) Close() error {
	if watch == nil || watch.connection == nil {
		return nil
	}
	var result error
	watch.closeOnce.Do(func() {
		result = watch.connection.Close(websocket.StatusNormalClosure, "client closed Inventory Watch")
	})
	return result
}
