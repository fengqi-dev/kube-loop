package exchange

import (
	"errors"
	"net"

	"github.com/fengqi-dev/kube-loop/internal/client/reverserelay"
	"github.com/fengqi-dev/kube-loop/internal/client/taskrelay"
)

type DialContextFunc = reverserelay.DialContextFunc

type Config struct {
	DialContext    DialContextFunc
	TrafficStreams TrafficStreamOpener
}

func NewManager(client Client, config Config) (*Manager, error) {
	if client == nil || config.TrafficStreams == nil {
		return nil, errors.New("exchange control client and Data Plane stream opener are required")
	}
	if config.DialContext == nil {
		dialer := &net.Dialer{}
		config.DialContext = dialer.DialContext
	}
	remoteGateway := gateway{client: client, streams: config.TrafficStreams, dial: config.DialContext}
	tasks, err := taskrelay.New(taskrelay.Config[Info]{
		Name: "exchange", Gateway: remoteGateway, Open: remoteGateway.open, Describe: describe,
	})
	if err != nil {
		return nil, err
	}
	return &Manager{Manager: tasks, client: client, gateway: remoteGateway}, nil
}
