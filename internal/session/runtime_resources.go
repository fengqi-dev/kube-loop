package session

import (
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
)

type runtimeResource struct {
	name   string
	closer io.Closer
}

// sessionRuntime owns resources created while establishing a connection.
// Resources are closed in reverse registration order so each dependent is
// torn down before the resource it uses. Close is idempotent because both
// explicit disconnect and failure paths can converge on the same runtime.
type sessionRuntime struct {
	closeOnce sync.Once
	resources []runtimeResource
	closeErr  error
}

func newSessionRuntime() *sessionRuntime {
	return &sessionRuntime{}
}

func (r *sessionRuntime) Add(name string, closer io.Closer) {
	if closer == nil {
		return
	}
	r.resources = append(r.resources, runtimeResource{name: name, closer: closer})
}

func (r *sessionRuntime) AddFunc(name string, close func()) {
	if close == nil {
		return
	}
	r.Add(name, closerFunc(close))
}

func (r *sessionRuntime) Close() error {
	r.closeOnce.Do(func() {
		var errs []error
		for _, resource := range slices.Backward(r.resources) {
			if err := resource.closer.Close(); err != nil {
				if errors.Is(err, net.ErrClosed) {
					continue
				}
				errs = append(errs, fmt.Errorf("close %s: %w", resource.name, err))
			}
		}
		r.resources = nil
		r.closeErr = errors.Join(errs...)
	})
	return r.closeErr
}

type closerFunc func()

func (function closerFunc) Close() error {
	function()
	return nil
}
