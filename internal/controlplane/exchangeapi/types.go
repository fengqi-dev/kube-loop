package exchangeapi

import "github.com/fengqi-dev/kube-loop/internal/protocol/servicemodel"

type Spec struct {
	Service      string                     `json:"service"`
	Ports        []servicemodel.Port        `json:"ports"`
	LocalTargets []servicemodel.LocalTarget `json:"localTargets,omitempty"`
}
