package service

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/ticketapi/entity"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
	"github.com/fengqi-dev/kube-loop/internal/utils"
)

const OperationTunnel = "tunnel"

var (
	ErrSessionExpiresSoon = errors.New(
		"session expires too soon to issue a RelayTicket",
	)
	ErrNoReadyDataPlane = errors.New("no ready Data Plane is available")
	ErrSigning          = errors.New("relay ticket issuance failed")
)

type RelayAllocator interface {
	Allocate(
		relaycontrol.AllocationRequest,
	) (relaycontrol.AllocationResponse, error)
}

type Config struct {
	Issuer            string
	TTL               time.Duration
	Now               func() time.Time
	Signer            *relayticket.Signer
	Allocator         RelayAllocator
	Topology          map[string]string
	Logger            *slog.Logger
	TrafficEncryption *bool
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
	issuer            string
	ttl               time.Duration
	now               func() time.Time
	signer            *relayticket.Signer
	allocator         RelayAllocator
	topology          map[string]string
	logger            *slog.Logger
	trafficEncryption bool
}

func New(config Config) (*Service, error) {
	config.Issuer = strings.TrimSpace(config.Issuer)
	if config.Signer == nil || config.Allocator == nil || config.Issuer == "" ||
		len(config.Issuer) > 512 {
		return nil, errors.New("relay ticket service configuration is invalid")
	}
	if config.TTL == 0 {
		config.TTL = relayticket.DefaultLifetime
	}
	if config.TTL < 15*time.Second || config.TTL > relayticket.MaximumLifetime {
		return nil, errors.New(
			"relay ticket TTL must be between 15 seconds and 1 minute",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if config.Logger == nil {
		config.Logger = slog.Default()
	}
	if config.TrafficEncryption == nil {
		value := true
		config.TrafficEncryption = &value
	}
	return &Service{
		issuer: config.Issuer, ttl: config.TTL, now: config.Now,
		signer: config.Signer, allocator: config.Allocator, topology: cloneTopology(config.Topology),
		logger: config.Logger, trafficEncryption: *config.TrafficEncryption,
	}, nil
}

func (service *Service) Issue(
	ctx context.Context,
	input IssueInput,
) (entity.Ticket, error) {
	startedAt := time.Now()
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
	allocation.TrafficEncryption = new(service.trafficEncryption)
	assignment, err := service.allocator.Allocate(allocation)
	if err != nil {
		return entity.Ticket{}, errors.Join(ErrNoReadyDataPlane, err)
	}
	if assignment.TrafficEncryption != service.trafficEncryption ||
		(service.trafficEncryption && assignment.NoisePublicKey == "") ||
		(!service.trafficEncryption && assignment.NoisePublicKey != "") {
		return entity.Ticket{}, errors.Join(
			ErrNoReadyDataPlane,
			errors.New("data plane traffic encryption capability does not match policy"),
		)
	}
	claims := relayticket.Claims{
		Version: relayticket.Version, Issuer: service.issuer, Audience: assignment.RelayID,
		IdentityID: input.IdentityID, Groups: append([]string(nil), input.Groups...), DeviceID: input.DeviceID,
		SessionID: input.SessionID, SessionGeneration: input.Generation,
		Namespace: input.Namespace, Operations: []string{OperationTunnel},
		NetworkSpecHash: input.NetworkSpecHash, TicketID: uuid.NewString(),
		IssuedAt: now.Unix(), NotBefore: now.Unix(), ExpiresAt: expiresAt.Unix(),
	}
	if service.trafficEncryption {
		claims.TrafficEncryption = new(true)
		claims.NoisePublicKey = assignment.NoisePublicKey
	}
	ticket, err := service.signer.Sign(claims)
	if err != nil {
		return entity.Ticket{}, errors.Join(ErrSigning, err)
	}
	service.logger.InfoContext(
		ctx,
		"RelayTicket issued",
		"operation", "relay.ticket.issue",
		"outcome", "success",
		"correlation_id", utils.CorrelationID(ctx),
		"duration_ms", time.Since(startedAt).Milliseconds(),
		"session_id", input.SessionID,
		"session_generation", input.Generation,
		"ticket_id", claims.TicketID,
		"relay_id", assignment.RelayID,
	)
	return entity.Ticket{
		TokenType: relayticket.Type, Value: ticket, ExpiresAt: expiresAt,
		DeviceID: input.DeviceID, RelayID: assignment.RelayID, Endpoint: assignment.Endpoint,
		TrafficEncryption: cloneBoolPointer(claims.TrafficEncryption),
		NoisePublicKey:    claims.NoisePublicKey,
	}, nil
}

func cloneBoolPointer(value *bool) *bool {
	if value == nil {
		return nil
	}
	result := *value
	return &result
}

func cloneTopology(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	return maps.Clone(source)
}
