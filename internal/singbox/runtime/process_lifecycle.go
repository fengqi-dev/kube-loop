package runtime

import (
	"context"
	"strings"
	"time"
)

func (p *Process) wait() {
	<-p.stopCh
	p.errMu.Lock()
	p.waitErr = nil
	p.errMu.Unlock()
	close(p.done)
}

func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		p.updateMu.Lock()
		defer p.updateMu.Unlock()
		p.specMu.Lock()
		p.closed = true
		p.specMu.Unlock()
		select {
		case <-p.done:
		default:
			// helperStop blocks until the helper has restored DNS and routes.
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			if p.helperStop != nil {
				_ = p.helperStop(ctx)
			}
			cancel()
			close(p.stopCh)
			select {
			case <-p.done:
			case <-time.After(20 * time.Second):
				select {
				case <-p.done:
				case <-time.After(2 * time.Second):
				}
			}
		}
		p.specMu.Lock()
		proxy := p.dnsProxy
		p.dnsProxy = nil
		p.specMu.Unlock()
		if proxy != nil {
			_ = proxy.Close()
		}
	})
	err := p.Err()
	if err != nil && !strings.Contains(strings.ToLower(err.Error()), "signal") {
		return err
	}
	return nil
}
