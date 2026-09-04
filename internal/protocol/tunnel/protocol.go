package tunnel

const (
	CommandTCP     byte = 1
	CommandUDP     byte = 2
	CommandControl byte = 3
	CommandTraffic byte = 4

	StatusOK    byte = 0
	StatusError byte = 1

	MaxDatagramSize = 65507
	maxHostSize     = 1024
	maxErrorSize    = 4096
	maxIDSize       = 256

	trafficModeExchange byte = 1
	trafficModeMirror   byte = 2
	trafficModePreview  byte = 3
	trafficTaskIDSize        = 36
	trafficOpenBodySize      = 1 + trafficTaskIDSize
)

const (
	TrafficModeExchange = "exchange"
	TrafficModeMirror   = "mirror"
	TrafficModePreview  = "preview"
)

var magic = [4]byte{'K', 'C', 'G', 2}
