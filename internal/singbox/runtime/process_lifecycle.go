package runtime

import (
	"context"
	"strings"
)

func (p *Process) wait() {
	<-p.stopCh
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
			ctx, cancel := context.WithTimeout(context.Background(), helperStopTimeout)
			var stopErr error
			if p.helperStop != nil {
				stopErr = p.helperStop(ctx)
			}
			cancel()
			p.errMu.Lock()
			p.waitErr = stopErr
			p.errMu.Unlock()
			close(p.stopCh)
			<-p.done
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
