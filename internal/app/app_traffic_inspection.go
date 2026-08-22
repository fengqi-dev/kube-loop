package app

import (
	"log"
	"path/filepath"

	clientdataplane "github.com/fengqi-dev/kube-loop/internal/client/dataplane"
	"github.com/fengqi-dev/kube-loop/internal/trafficinspect"
)

func newTrafficInspection(version, profilePath string) (
	clientdataplane.TrafficInspectionConfig,
	*trafficinspect.RingBufferSink,
	*trafficinspect.SwitchableSink,
) {
	const enabled = true
	config := clientdataplane.TrafficInspectionConfig{Enabled: enabled}
	events, err := trafficinspect.NewRingBufferSink(2_000)
	if err != nil {
		log.Printf("traffic inspection events: %v", err)
		return config, nil, nil
	}
	config.Policy = trafficinspect.CapturePolicy{CaptureBodies: true, MaxBodyBytes: 4 << 20}
	config.Protobuf = trafficinspect.NewProtobufDecoder()
	var sink trafficinspect.Sink = events
	layout, err := appUserLayout(version, profilePath)
	if err != nil {
		log.Printf("traffic inspection output: resolve user layout: %v", err)
		return switchableTrafficInspection(config, events, sink)
	}
	path := filepath.Join(layout.DataDir(), "traffic-inspection", "events.jsonl")
	fileSink, err := trafficinspect.NewDailyJSONLFileSink(path)
	if err != nil {
		log.Printf("traffic inspection output: %v", err)
		return switchableTrafficInspection(config, events, sink)
	}
	combined, err := trafficinspect.NewMultiSink(events, fileSink)
	if err != nil {
		log.Printf("traffic inspection output: %v", err)
		return switchableTrafficInspection(config, events, events)
	}
	sink = combined
	config.OnSinkError = func(err error) {
		log.Printf("traffic inspection output: %v", err)
	}
	return switchableTrafficInspection(config, events, sink)
}

func switchableTrafficInspection(
	config clientdataplane.TrafficInspectionConfig,
	events *trafficinspect.RingBufferSink,
	sink trafficinspect.Sink,
) (clientdataplane.TrafficInspectionConfig, *trafficinspect.RingBufferSink, *trafficinspect.SwitchableSink) {
	switchable, err := trafficinspect.NewSwitchableSink(sink, true)
	if err != nil {
		log.Printf("traffic inspection switch: %v", err)
		return config, events, nil
	}
	config.Sink = switchable
	config.IsEnabled = switchable.Enabled
	return config, events, switchable
}
