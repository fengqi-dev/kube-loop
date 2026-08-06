package intercept

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

const (
	controlRedialAttempts   = 6
	controlRedialRetryDelay = 25 * time.Millisecond
)

// controlSession owns the current Gateway control channel and its recovery
// state. Manager.mu protects state-changing calls.
type controlSession struct {
	client     *controlClient
	lost       chan struct{}
	recovering bool
	generation uint64
	onReady    func(interceptID string, network byte, streamID uint64)
}

func newControlSession(
	onReady func(interceptID string, network byte, streamID uint64),
) *controlSession {
	return &controlSession{onReady: onReady}
}

func (s *controlSession) connect(ctx context.Context, address string) error {
	client, lost, err := s.open(ctx, address)
	if err != nil {
		return err
	}
	s.generation++
	s.attach(client, lost)
	return nil
}

func (s *controlSession) open(
	ctx context.Context,
	address string,
) (*controlClient, chan struct{}, error) {
	lost := make(chan struct{})
	client, err := dialControl(
		ctx,
		address,
		s.onReady,
		sync.OnceFunc(func() { close(lost) }),
	)
	if err != nil {
		return nil, nil, err
	}
	return client, lost, nil
}

func (s *controlSession) redial(
	ctx context.Context,
	address string,
	registrations []controlRegistration,
) (*controlClient, chan struct{}, error) {
	var lastErr error
	for attempt := range controlRedialAttempts {
		if attempt > 0 {
			delay := time.Duration(attempt) * controlRedialRetryDelay
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, nil, ctx.Err()
			case <-timer.C:
			}
		}

		client, lost, err := s.open(ctx, address)
		if err != nil {
			return nil, nil, err
		}
		registered := make([]string, 0, len(registrations))
		var registrationErr error
		for _, registration := range registrations {
			if err := client.register(
				registration.id,
				registration.network,
				registration.listenPort,
			); err != nil {
				registrationErr = fmt.Errorf("re-register %s: %w", registration.id, err)
				break
			}
			registered = append(registered, registration.id)
		}
		if registrationErr == nil {
			return client, lost, nil
		}

		for index := len(registered) - 1; index >= 0; index-- {
			_ = client.unregister(registered[index])
		}
		_ = client.close()
		lastErr = registrationErr
		if !isTransientControlRegistrationError(registrationErr) {
			return nil, nil, registrationErr
		}
	}
	return nil, nil, lastErr
}

func isTransientControlRegistrationError(err error) bool {
	message := err.Error()
	return strings.Contains(message, "already registered") ||
		strings.Contains(message, "already in use")
}

func (s *controlSession) attach(client *controlClient, lost chan struct{}) {
	s.client = client
	s.lost = lost
	s.recovering = false
}

func (s *controlSession) ready() bool {
	return s.client != nil && !s.recovering
}

func (s *controlSession) current() *controlClient {
	return s.client
}

func (s *controlSession) snapshot() (*controlClient, uint64, bool) {
	return s.client, s.generation, s.ready()
}

func (s *controlSession) matches(client *controlClient, generation uint64) bool {
	return s.client == client &&
		s.generation == generation &&
		s.ready() &&
		!s.client.closed.Load()
}

func (s *controlSession) lostSignal() <-chan struct{} {
	return s.lost
}

func (s *controlSession) beginRecovery() (*controlClient, uint64) {
	s.generation++
	s.recovering = true
	old := s.client
	s.client = nil
	return old, s.generation
}

func (s *controlSession) finishRecovery(
	generation uint64,
	client *controlClient,
	lost chan struct{},
) bool {
	if generation != s.generation || !s.recovering {
		return false
	}
	s.attach(client, lost)
	return true
}

func (s *controlSession) recoveryFailed(generation uint64) {
	if generation == s.generation {
		s.recovering = false
	}
}

func (s *controlSession) stop() *controlClient {
	s.generation++
	client := s.client
	s.client = nil
	s.recovering = false
	return client
}

// close is used by package tests to simulate an unexpected control loss.
func (s *controlSession) close() error {
	if s.client == nil {
		return nil
	}
	return s.client.close()
}
