package websocketmux

import (
	"context"
	"time"

	"github.com/xtaci/smux"
)

func (forwarder *Forwarder) keepAlive(item *pooledSession) {
	ticker := time.NewTicker(defaultKeepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-forwarder.ctx.Done():
			return
		case <-item.session.CloseChan():
			forwarder.discard(item)
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(forwarder.ctx, 10*time.Second)
			err := item.ws.Ping(ctx)
			cancel()
			if err != nil {
				forwarder.discard(item)
				return
			}
		}
	}
}

func smuxConfig() *smux.Config {
	config := smux.DefaultConfig()
	config.Version = 2
	config.KeepAliveInterval = defaultKeepAliveInterval
	config.KeepAliveTimeout = defaultKeepAliveTimeout
	config.MaxReceiveBuffer = 4 * 1024 * 1024
	config.MaxStreamBuffer = 512 * 1024
	return config
}
