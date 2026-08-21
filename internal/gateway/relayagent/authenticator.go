package relayagent

import (
	"crypto/ed25519"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relaycontrol"
	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

type TicketAuthenticatorConfig struct {
	RequiredOperation string
	ReplayEntries     int
	ClockSkew         time.Duration
	Now               func() time.Time
}

type TicketAuthenticator struct {
	mu                   sync.RWMutex
	config               TicketAuthenticatorConfig
	requestVerifier      *relayticket.RequestVerifier
	relayID              string
	issuer               string
	keyGeneration        uint64
	revocationGeneration uint64
}

func NewTicketAuthenticator(config TicketAuthenticatorConfig) (*TicketAuthenticator, error) {
	if config.Now == nil {
		config.Now = time.Now
	}
	// Validate non-key verifier inputs without inventing a bootstrap trust key.
	if config.RequiredOperation == "" {
		return nil, errors.New("dynamic RelayTicket authenticator configuration is invalid")
	}
	if _, err := relayticket.NewReplayGuard(config.ReplayEntries, config.Now); err != nil {
		return nil, err
	}
	return &TicketAuthenticator{config: config}, nil
}

func (authenticator *TicketAuthenticator) Apply(
	issuer, relayID string,
	keys relaycontrol.VerificationKeySet,
	revocations relaycontrol.RevocationSummary,
) error {
	if authenticator == nil {
		return errors.New("dynamic RelayTicket authenticator is unavailable")
	}
	now := authenticator.config.Now().UTC()
	parsed, err := keys.Parse(now)
	if err != nil {
		return err
	}
	if err := revocations.Validate(now); err != nil {
		return err
	}
	verificationKeys := make(map[string]ed25519.PublicKey, len(parsed))
	validity := make(map[string]relayticket.KeyValidity, len(parsed))
	for keyID, key := range parsed {
		verificationKeys[keyID] = append(ed25519.PublicKey(nil), key.PublicKey...)
		validity[keyID] = relayticket.KeyValidity{NotBefore: key.NotBefore, NotAfter: key.NotAfter}
	}
	verifier, err := relayticket.NewVerifier(relayticket.VerifierConfig{
		Keys: verificationKeys, KeyValidity: validity,
		Issuer: issuer, Audience: relayID,
		RequiredOperation: authenticator.config.RequiredOperation,
		ClockSkew:         authenticator.config.ClockSkew, Now: authenticator.config.Now,
	})
	if err != nil {
		return err
	}
	revocationSnapshot := revocations
	checker := relayticket.RevocationChecker(func(claims relayticket.Claims, checkedAt time.Time) bool {
		if revocationSnapshot.Generation > 0 && !revocationSnapshot.ValidUntil.After(checkedAt) {
			return true
		}
		return revocationSnapshot.Revokes(claims.SessionID, claims.SessionGeneration, checkedAt)
	})

	authenticator.mu.Lock()
	defer authenticator.mu.Unlock()
	if authenticator.relayID != "" && authenticator.relayID != relayID {
		return errors.New("relay identity changed within one Data Plane process")
	}
	if authenticator.issuer != "" && authenticator.issuer != issuer {
		return errors.New("RelayTicket issuer changed within one Data Plane process")
	}
	if keys.Generation < authenticator.keyGeneration || revocations.Generation < authenticator.revocationGeneration {
		return errors.New("relay control generation moved backwards")
	}
	if authenticator.requestVerifier == nil {
		replay, err := relayticket.NewReplayGuard(authenticator.config.ReplayEntries, authenticator.config.Now)
		if err != nil {
			return err
		}
		requestVerifier, err := relayticket.NewRequestVerifier(verifier, replay)
		if err != nil {
			return err
		}
		if err := requestVerifier.Update(verifier, checker); err != nil {
			return err
		}
		authenticator.requestVerifier = requestVerifier
	} else if err := authenticator.requestVerifier.Update(verifier, checker); err != nil {
		return err
	}
	authenticator.relayID = relayID
	authenticator.issuer = issuer
	authenticator.keyGeneration = keys.Generation
	authenticator.revocationGeneration = revocations.Generation
	return nil
}

func (authenticator *TicketAuthenticator) Verify(request *http.Request) (relayticket.Claims, error) {
	if authenticator == nil {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	authenticator.mu.RLock()
	requestVerifier := authenticator.requestVerifier
	authenticator.mu.RUnlock()
	if requestVerifier == nil {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	return requestVerifier.Verify(request)
}

func (authenticator *TicketAuthenticator) AppliedGenerations() (uint64, uint64) {
	if authenticator == nil {
		return 0, 0
	}
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	return authenticator.keyGeneration, authenticator.revocationGeneration
}

func (authenticator *TicketAuthenticator) RelayID() string {
	authenticator.mu.RLock()
	defer authenticator.mu.RUnlock()
	return authenticator.relayID
}
