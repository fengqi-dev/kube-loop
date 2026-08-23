package runtime

import (
	"context"
	"errors"
	"slices"
	"strings"
)

func (p *Process) ReadLogs(ctx context.Context) ([]string, error) {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	if p.readLogs == nil {
		return nil, errors.New("privileged log reader is unavailable")
	}
	data, nextOffset, err := p.readLogs(ctx, p.SessionID(), p.logOffset)
	if err != nil {
		return nil, err
	}
	if nextOffset < p.logOffset {
		p.logPending = ""
		p.logHistory = nil
	}
	p.logOffset = nextOffset
	data = p.logPending + data
	parts := strings.Split(data, "\n")
	if !strings.HasSuffix(data, "\n") {
		p.logPending = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	} else {
		p.logPending = ""
		parts = parts[:len(parts)-1]
	}
	p.logHistory = append(p.logHistory, parts...)
	if len(p.logHistory) > maxDataPlaneLogLines {
		p.logHistory = slices.Clone(p.logHistory[len(p.logHistory)-maxDataPlaneLogLines:])
	}
	return slices.Clone(p.logHistory), nil
}
