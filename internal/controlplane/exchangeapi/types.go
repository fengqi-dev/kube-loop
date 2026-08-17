package exchangeapi

import "github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"

type Spec struct {
	Service string              `json:"service"`
	Ports   []trafficmodel.Port `json:"ports"`
}
