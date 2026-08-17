package trafficcontrol

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"
	"github.com/google/uuid"
)

const (
	InternalPathPrefix = "/internal/v1/traffic"
	PublicPathPrefix   = "/traffic/v1"
	MaximumBodyBytes   = 256 << 10
)

type Mode string

const (
	ModeExchange Mode = "exchange"
	ModeMirror   Mode = "mirror"
	ModePreview  Mode = "preview"
)

type Identity struct {
	IdentityID        string   `json:"identityId"`
	Groups            []string `json:"groups,omitempty"`
	DeviceID          string   `json:"deviceId"`
	SessionID         string   `json:"sessionId"`
	SessionGeneration uint64   `json:"sessionGeneration"`
	Namespace         string   `json:"namespace"`
}

type ClaimRequest struct {
	Mode     Mode     `json:"mode"`
	TaskID   string   `json:"taskId"`
	Identity Identity `json:"identity"`
}

type ClaimResponse struct {
	Mode    Mode                `json:"mode"`
	TaskID  string              `json:"taskId"`
	Service string              `json:"service"`
	Ports   []trafficmodel.Port `json:"ports"`
}

type ListenerPort struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	ListenPort  int32  `json:"listenPort"`
	Protocol    string `json:"protocol"`
}

type PrepareRequest struct {
	Mode      Mode           `json:"mode"`
	TaskID    string         `json:"taskId"`
	Identity  Identity       `json:"identity"`
	RelayID   string         `json:"relayId"`
	GatewayIP string         `json:"gatewayIp"`
	Ports     []ListenerPort `json:"ports"`
}

type BackendTarget struct {
	Address string `json:"address"`
	Port    int32  `json:"port"`
}

type BackendSet struct {
	Name        string          `json:"name,omitempty"`
	ServicePort int32           `json:"servicePort"`
	Protocol    string          `json:"protocol"`
	Targets     []BackendTarget `json:"targets"`
}

type PrepareResponse struct {
	ClusterIP string       `json:"clusterIp,omitempty"`
	Backends  []BackendSet `json:"backends,omitempty"`
}

type HeartbeatRequest struct {
	Mode    Mode   `json:"mode"`
	TaskID  string `json:"taskId"`
	RelayID string `json:"relayId"`
}

type HeartbeatResponse struct {
	Stop bool `json:"stop"`
}

type FinishRequest struct {
	Mode    Mode   `json:"mode"`
	TaskID  string `json:"taskId"`
	RelayID string `json:"relayId"`
	Failed  bool   `json:"failed"`
	Reason  string `json:"reason,omitempty"`
}

type FinishResponse struct {
	State string `json:"state"`
}

func (request ClaimRequest) Validate() error {
	if !validMode(request.Mode) || !validTaskID(request.TaskID) || request.Identity.Validate() != nil {
		return errors.New("traffic claim request is invalid")
	}
	return nil
}

func (identity Identity) Validate() error {
	if !validText(identity.IdentityID, 256) || !validText(identity.DeviceID, 256) ||
		!validTaskID(identity.SessionID) || identity.SessionGeneration == 0 ||
		!validText(identity.Namespace, 63) || len(identity.Groups) > 128 {
		return errors.New("traffic identity is invalid")
	}
	for _, group := range identity.Groups {
		if !validText(group, 256) {
			return errors.New("traffic identity group is invalid")
		}
	}
	return nil
}

func (request PrepareRequest) Validate() error {
	if !validMode(request.Mode) || !validTaskID(request.TaskID) || request.Identity.Validate() != nil ||
		!validText(request.RelayID, 128) || net.ParseIP(strings.TrimSpace(request.GatewayIP)) == nil ||
		len(request.Ports) == 0 || len(request.Ports) > 64 {
		return errors.New("traffic prepare request is invalid")
	}
	seen := make(map[string]struct{}, len(request.Ports))
	for _, port := range request.Ports {
		protocol := strings.ToLower(strings.TrimSpace(port.Protocol))
		if port.ServicePort < 1 || port.ServicePort > 65535 || port.ListenPort < 1 || port.ListenPort > 65535 ||
			(protocol != "tcp" && protocol != "udp") {
			return errors.New("traffic listener port is invalid")
		}
		key := protocol + "/" + strconv.Itoa(int(port.ServicePort))
		if _, exists := seen[key]; exists {
			return errors.New("traffic listener port is duplicated")
		}
		seen[key] = struct{}{}
	}
	return nil
}

func (request HeartbeatRequest) Validate() error {
	if !validMode(request.Mode) || !validTaskID(request.TaskID) || !validText(request.RelayID, 128) {
		return errors.New("traffic heartbeat request is invalid")
	}
	return nil
}

func (request FinishRequest) Validate() error {
	if !validMode(request.Mode) || !validTaskID(request.TaskID) || !validText(request.RelayID, 128) ||
		len(request.Reason) > 512 || strings.TrimSpace(request.Reason) != request.Reason {
		return errors.New("traffic finish request is invalid")
	}
	return nil
}

func validMode(mode Mode) bool {
	return mode == ModeExchange || mode == ModeMirror || mode == ModePreview
}

func validTaskID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && strings.TrimSpace(value) == value
}
