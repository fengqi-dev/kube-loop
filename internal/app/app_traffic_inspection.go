package app

import (
	"sync/atomic"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func newTrafficInspection() (clientdataplane.TrafficInspectionConfig, *atomic.Bool) {
	const enabled = true
	state := &atomic.Bool{}
	state.Store(enabled)
	config := clientdataplane.TrafficInspectionConfig{
		Enabled:   enabled,
		IsEnabled: state.Load,
		Policy:    trafficinspect.CapturePolicy{CaptureBodies: true, MaxBodyBytes: 4 << 20},
		Protobuf:  trafficinspect.NewProtobufDecoder(),
	}
	return config, state
}
