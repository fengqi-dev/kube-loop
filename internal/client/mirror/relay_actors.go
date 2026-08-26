package mirror

import (
	"context"
	"errors"
)

func (relay *localRelay) createActor(
	ctx context.Context,
	id uint64,
	protocol string,
	servicePort int32,
) (*shadowActor, error) {
	target, exists := relay.targets[targetKey(protocol, servicePort)]
	if !exists {
		return nil, errors.New("gateway requested an unconfigured local Mirror target")
	}
	relay.mu.Lock()
	defer relay.mu.Unlock()
	if relay.streams[id] != nil {
		return nil, errors.New("gateway reused an active Mirror stream ID")
	}
	if _, wasDropped := relay.dropped[id]; wasDropped {
		return nil, errors.New("gateway reused an active Mirror stream ID")
	}
	actor := newShadowActor(ctx, target, relay.dial, relay.config)
	relay.streams[id] = actor
	relay.wg.Go(func() {
		<-actor.done
		relay.mu.Lock()
		defer relay.mu.Unlock()
		if relay.streams[id] == actor {
			delete(relay.streams, id)
			relay.markDroppedLocked(id)
		}
	})
	return actor, nil
}

func (relay *localRelay) actorState(id uint64) (*shadowActor, bool) {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	_, dropped := relay.dropped[id]
	return relay.streams[id], dropped
}

func (relay *localRelay) drop(id uint64, actor *shadowActor) {
	relay.mu.Lock()
	if relay.streams[id] == actor {
		delete(relay.streams, id)
		relay.markDroppedLocked(id)
	}
	relay.mu.Unlock()
	actor.cancel()
}

func (relay *localRelay) markDroppedLocked(id uint64) {
	if len(relay.dropped) >= maxDroppedShadowStreams {
		for candidate := range relay.dropped {
			delete(relay.dropped, candidate)
			break
		}
	}
	relay.dropped[id] = struct{}{}
}

func (relay *localRelay) remove(id uint64) {
	relay.mu.Lock()
	actor := relay.streams[id]
	delete(relay.streams, id)
	delete(relay.dropped, id)
	relay.mu.Unlock()
	if actor != nil {
		actor.Finish()
	}
}

func (relay *localRelay) closeAll() {
	relay.mu.Lock()
	actors := make([]*shadowActor, 0, len(relay.streams))
	for _, actor := range relay.streams {
		actors = append(actors, actor)
	}
	relay.streams = make(map[uint64]*shadowActor)
	relay.dropped = make(map[uint64]struct{})
	relay.mu.Unlock()
	for _, actor := range actors {
		actor.Close()
	}
}
