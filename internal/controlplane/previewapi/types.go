package previewapi

import "github.com/fengqi-dev/kube-loop/internal/protocol/trafficmodel"

type Spec struct {
	Name  string              `json:"name"`
	Ports []trafficmodel.Port `json:"ports"`
}
