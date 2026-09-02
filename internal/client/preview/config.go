package preview

import (
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
)

type DialContextFunc = reverserelay.DialContextFunc

type Config struct {
	DialContext    DialContextFunc
	TrafficStreams TrafficStreamOpener
}

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil || config.TrafficStreams == nil {
		return nil, errors.New("preview control client and Data Plane stream opener are required")
	}
	if config.DialContext == nil {
		dialer := &net.Dialer{}
		config.DialContext = dialer.DialContext
	}
	return &Manager{
		client: client, streams: config.TrafficStreams, dial: config.DialContext,
		active: make(map[string]*activePreview),
	}, nil
}
