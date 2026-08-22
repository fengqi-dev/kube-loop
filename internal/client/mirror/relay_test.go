package mirror

import (
	"context"
	"errors"
	"testing"
)

func TestLocalRelayDropCancelsAndMarksActor(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	actor := &shadowActor{ctx: ctx, cancel: cancel}
	relay := &localRelay{
		streams: map[uint64]*shadowActor{7: actor},
		dropped: make(map[uint64]struct{}),
	}

	relay.drop(7, actor)
	if relay.streams[7] != nil {
		t.Fatal("dropped actor remained active")
	}
	if _, ok := relay.dropped[7]; !ok {
		t.Fatal("dropped stream ID was not retained")
	}
	if !errors.Is(actor.ctx.Err(), context.Canceled) {
		t.Fatalf("actor context error = %v", actor.ctx.Err())
	}
}

func TestLocalRelayDroppedStreamSetIsBounded(t *testing.T) {
	relay := &localRelay{dropped: make(map[uint64]struct{}, maxDroppedShadowStreams)}
	for id := range maxDroppedShadowStreams {
		relay.dropped[uint64(id)] = struct{}{}
	}
	relay.markDroppedLocked(maxDroppedShadowStreams + 1)
	if len(relay.dropped) != maxDroppedShadowStreams {
		t.Fatalf("dropped stream IDs = %d, want %d", len(relay.dropped), maxDroppedShadowStreams)
	}
	if _, ok := relay.dropped[maxDroppedShadowStreams+1]; !ok {
		t.Fatal("new dropped stream ID was not retained")
	}
}
