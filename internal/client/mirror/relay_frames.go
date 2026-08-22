package mirror

import (
	"context"
	"errors"

	"github.com/fengqi-dev/kube-loop/internal/protocol/mirrorstream"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficstream"
)

func newLocalRelay(
	connection *trafficstream.FrameConn,
	targets []LocalTarget,
	dial DialContextFunc,
	config Config,
) *localRelay {
	targetMap := make(map[string]LocalTarget, len(targets))
	for _, target := range targets {
		targetMap[targetKey(target.Protocol, target.ServicePort)] = target
	}
	return &localRelay{
		stream: connection, targets: targetMap, dial: dial, config: config,
		streams: make(map[uint64]*shadowActor), dropped: make(map[uint64]struct{}),
	}
}

func (relay *localRelay) readReady(ctx context.Context) error {
	encoded, err := relay.stream.ReadFrame(ctx)
	if err != nil {
		return err
	}
	frame, err := mirrorstream.Decode(encoded)
	if err != nil || frame.Type != mirrorstream.Ready {
		return errors.New("gateway returned an invalid Mirror readiness frame")
	}
	return nil
}

func (relay *localRelay) run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		relay.closeAll()
		relay.wg.Wait()
		_ = relay.stream.Close()
	}()
	for {
		encoded, err := relay.stream.ReadFrame(ctx)
		if err != nil {
			return err
		}
		frame, err := mirrorstream.Decode(encoded)
		if err != nil {
			return err
		}
		switch frame.Type {
		case mirrorstream.Open:
			servicePort, err := mirrorServicePort(frame.ServicePort)
			if err != nil {
				return err
			}
			if _, err := relay.createActor(
				ctx,
				frame.StreamID,
				mirrorProtocolTCP,
				servicePort,
			); err != nil {
				return err
			}
		case mirrorstream.Data:
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil || actor.target.Protocol != mirrorProtocolTCP {
				return errors.New("gateway referenced an unknown local Mirror TCP stream")
			}
			if !actor.enqueue(shadowMessage{payload: frame.Payload}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.CloseWrite:
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil || actor.target.Protocol != mirrorProtocolTCP {
				return errors.New("gateway referenced an unknown local Mirror TCP stream")
			}
			if !actor.enqueue(shadowMessage{closeWrite: true}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.Datagram:
			servicePort, err := mirrorServicePort(frame.ServicePort)
			if err != nil {
				return err
			}
			actor, dropped := relay.actorState(frame.StreamID)
			if dropped {
				continue
			}
			if actor == nil {
				actor, err = relay.createActor(ctx, frame.StreamID, mirrorProtocolUDP, servicePort)
				if err != nil {
					return err
				}
			}
			if actor.target.Protocol != mirrorProtocolUDP || actor.target.ServicePort != servicePort {
				return errors.New("gateway changed a local Mirror UDP target")
			}
			if !actor.enqueue(shadowMessage{payload: frame.Payload}) {
				relay.drop(frame.StreamID, actor)
			}
		case mirrorstream.Close:
			relay.remove(frame.StreamID)
		case mirrorstream.Stop:
			return nil
		case mirrorstream.Ready:
			return errors.New("gateway sent duplicate Mirror readiness")
		default:
			return errors.New("gateway sent a client-only Mirror frame")
		}
	}
}

func mirrorServicePort(value uint32) (int32, error) {
	if value == 0 || value > 65535 {
		return 0, errors.New("gateway supplied an invalid Mirror service port")
	}
	return int32(value), nil
}

func (relay *localRelay) stop(ctx context.Context) error {
	encoded, err := mirrorstream.Encode(mirrorstream.Frame{Type: mirrorstream.Stop})
	if err != nil {
		return err
	}
	return relay.stream.WriteFrame(ctx, encoded)
}
