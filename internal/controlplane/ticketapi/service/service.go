package service

import (
	"context"
	"errors"
	"maps"
	"strings"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/entity"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/google/uuid"
)

const OperationTunnel = "tunnel"

var (
	ErrSessionExpiresSoon = errors.New("Session expires too soon to issue a RelayTicket")
	ErrNoReadyDataPlane   = errors.New("no ready Data Plane is available")
	ErrSigning            = errors.New("RelayTicket issuance failed")
)

type RelayAllocator interface {
	Allocate(relaycontrol.AllocationRequest) (relaycontrol.AllocationResponse, error)
}

type Config struct {
	Issuer    string
	TTL       time.Duration
	Now       func() time.Time
	Signer    *relayticket.Signer
	Allocator RelayAllocator
	Topology  map[string]string
}

type IssueInput struct {
	IdentityID       string
	Groups           []string
	DeviceID         string
	SessionID        string
	Generation       uint64
	Namespace        string
	NetworkSpecHash  string
	SessionExpiresAt time.Time
}

type Service struct {
	issuer    string
	ttl       time.Duration
	now       func() time.Time
	signer    *relayticket.Signer
	allocator RelayAllocator
	topology  map[string]string
}

func New(config Config) (*Service, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	if config.Signer == nil || config.Allocator == nil || config.Issuer == "" || len(config.Issuer) > 512 {
		return nil, errors.New("RelayTicket service configuration is invalid")
	}
	if config.TTL == 0 {
		config.TTL = relayticket.DefaultLifetime
	}
	if config.TTL < 15*time.Second || config.TTL > relayticket.MaximumLifetime {
		return nil, errors.New("RelayTicket TTL must be between 15 seconds and 2 minutes")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{
		issuer: config.Issuer, ttl: config.TTL, now: config.Now,
		signer: config.Signer, allocator: config.Allocator, topology: cloneTopology(config.Topology),
	}, nil
}

func (service *Service) Issue(_ context.Context, input IssueInput) (entity.Ticket, error) {
	now := service.now().UTC().Truncate(time.Second)
	expiresAt := now.Add(service.ttl)
	if input.SessionExpiresAt.Before(expiresAt) {
		expiresAt = input.SessionExpiresAt.UTC().Truncate(time.Second)
	}
	if !expiresAt.After(now.Add(5 * time.Second)) {
		return entity.Ticket{}, ErrSessionExpiresSoon
	}
	allocation := relaycontrol.NewAllocationRequest()
	allocation.SessionID = input.SessionID
	allocation.Generation = input.Generation
	allocation.NetworkSpecHash = input.NetworkSpecHash
	allocation.Topology = cloneTopology(service.topology)
	assignment, err := service.allocator.Allocate(allocation)
	if err != nil {
		return entity.Ticket{}, errors.Join(ErrNoReadyDataPlane, err)
	}
	claims := relayticket.Claims{
		Version: relayticket.Version, Issuer: service.issuer, Audience: assignment.RelayID,
		IdentityID: input.IdentityID, Groups: append([]string(nil), input.Groups...), DeviceID: input.DeviceID,
		SessionID: input.SessionID, SessionGeneration: input.Generation,
		Namespace: input.Namespace, Operations: []string{OperationTunnel},
		NetworkSpecHash: input.NetworkSpecHash, TicketID: uuid.NewString(),
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: expiresAt.Unix(),
	}
	ticket, err := service.signer.Sign(claims)
	if err != nil {
		return entity.Ticket{}, errors.Join(ErrSigning, err)
	}
	return entity.Ticket{
		TokenType: relayticket.Type, Value: ticket, ExpiresAt: expiresAt,
		DeviceID: input.DeviceID, RelayID: assignment.RelayID, Endpoint: assignment.Endpoint,
	}, nil
}

func cloneTopology(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	result := make(map[string]string, len(source))
	maps.Copy(result, source)
	return result
}
