package relaybearer

import (
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/protocol/relayticket"
)

const DefaultReplayEntries = 65536

type RequestVerifier struct {
	mu          sync.RWMutex
	verifier    *relayticket.Verifier
	replay      *ReplayGuard
	generations *SessionGenerationGuard
	revocations RevocationChecker
}

type RevocationChecker func(relayticket.Claims, time.Time) bool

func NewRequestVerifier(verifier *relayticket.Verifier, replay *ReplayGuard) (*RequestVerifier, error) {
	if verifier == nil || replay == nil {
		return nil, errors.New("relay ticket request verifier configuration is invalid")
	}
	generations, err := NewSessionGenerationGuard(replay.maxEntries, replay.now)
	if err != nil {
		return nil, err
	}
	return &RequestVerifier{verifier: verifier, replay: replay, generations: generations}, nil
}

func (verifier *RequestVerifier) Update(next *relayticket.Verifier, revocations RevocationChecker) error {
	if verifier == nil || next == nil {
		return errors.New("relay ticket request verifier update is invalid")
	}
	verifier.mu.Lock()
	verifier.verifier = next
	verifier.revocations = revocations
	verifier.mu.Unlock()
	return nil
}

func (verifier *RequestVerifier) Verify(request *http.Request) (relayticket.Claims, error) {
	return verifier.verify(request, true)
}

// VerifyReusable validates a Session-scoped transport ticket without consuming
// its replay entry. Standard proxy clients may reconnect a WebSocket using
// static headers; expiry, revocation and generation fencing still apply.
func (verifier *RequestVerifier) VerifyReusable(request *http.Request) (relayticket.Claims, error) {
	return verifier.verify(request, false)
}

func (verifier *RequestVerifier) verify(request *http.Request, consumeReplay bool) (relayticket.Claims, error) {
	if verifier == nil || request == nil {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	headers := request.Header.Values("Authorization")
	if len(headers) != 1 {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	parts := strings.Fields(headers[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	verifier.mu.RLock()
	ticketVerifier := verifier.verifier
	revocations := verifier.revocations
	verifier.mu.RUnlock()
	claims, err := ticketVerifier.Verify(parts[1])
	expiresAt := time.Unix(claims.ExpiresAt, 0)
	if err != nil || (revocations != nil && revocations(claims, verifier.replay.now().UTC())) ||
		(consumeReplay && !verifier.replay.Consume(claims.TicketID, expiresAt)) ||
		!verifier.generations.Accept(claims.SessionID, claims.SessionGeneration, expiresAt) {
		return relayticket.Claims{}, relayticket.ErrInvalid
	}
	return claims, nil
}

type sessionGenerationEntry struct {
	generation uint64
	expiresAt  int64
}

// SessionGenerationGuard remembers the newest generation seen for a Session
// until all tickets from that window have expired. It never evicts a live entry
// to make room, so capacity pressure fails closed.
type SessionGenerationGuard struct {
	mu         sync.Mutex
	entries    map[string]sessionGenerationEntry
	maxEntries int
	now        func() time.Time
}

func NewSessionGenerationGuard(maxEntries int, now func() time.Time) (*SessionGenerationGuard, error) {
	if maxEntries == 0 {
		maxEntries = DefaultReplayEntries
	}
	if maxEntries < 1 || maxEntries > 1_000_000 {
		return nil, errors.New("RelayTicket Session generation cache size is invalid")
	}
	if now == nil {
		now = time.Now
	}
	return &SessionGenerationGuard{
		entries: make(map[string]sessionGenerationEntry), maxEntries: maxEntries, now: now,
	}, nil
}

func (guard *SessionGenerationGuard) Accept(sessionID string, generation uint64, expiresAt time.Time) bool {
	if guard == nil || !relayticket.ValidIdentifier(sessionID, 128) || generation == 0 {
		return false
	}
	now := guard.now().UTC().Unix()
	expires := expiresAt.UTC().Unix()
	if expires <= now {
		return false
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if current, exists := guard.entries[sessionID]; exists {
		if current.expiresAt <= now {
			delete(guard.entries, sessionID)
		} else {
			if generation < current.generation {
				return false
			}
			if generation > current.generation {
				current.generation = generation
			}
			if expires > current.expiresAt {
				current.expiresAt = expires
			}
			guard.entries[sessionID] = current
			return true
		}
	}
	if len(guard.entries) >= guard.maxEntries {
		for existingID, current := range guard.entries {
			if current.expiresAt <= now {
				delete(guard.entries, existingID)
			}
		}
	}
	if len(guard.entries) >= guard.maxEntries {
		return false
	}
	guard.entries[sessionID] = sessionGenerationEntry{generation: generation, expiresAt: expires}
	return true
}

type ReplayGuard struct {
	mu         sync.Mutex
	expires    map[string]int64
	maxEntries int
	now        func() time.Time
}

func NewReplayGuard(maxEntries int, now func() time.Time) (*ReplayGuard, error) {
	if maxEntries == 0 {
		maxEntries = DefaultReplayEntries
	}
	if maxEntries < 1 || maxEntries > 1_000_000 {
		return nil, errors.New("RelayTicket replay cache size is invalid")
	}
	if now == nil {
		now = time.Now
	}
	return &ReplayGuard{expires: make(map[string]int64), maxEntries: maxEntries, now: now}, nil
}

func (guard *ReplayGuard) Consume(ticketID string, expiresAt time.Time) bool {
	if guard == nil || !relayticket.ValidIdentifier(ticketID, 128) {
		return false
	}
	now := guard.now().UTC().Unix()
	expires := expiresAt.UTC().Unix()
	if expires <= now {
		return false
	}
	guard.mu.Lock()
	defer guard.mu.Unlock()
	if _, exists := guard.expires[ticketID]; exists {
		return false
	}
	if len(guard.expires) >= guard.maxEntries {
		for existingID, existingExpiry := range guard.expires {
			if existingExpiry <= now {
				delete(guard.expires, existingID)
			}
		}
	}
	if len(guard.expires) >= guard.maxEntries {
		return false
	}
	guard.expires[ticketID] = expires
	return true
}
