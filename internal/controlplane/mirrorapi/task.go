package mirrorapi

import (
	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/trafficapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/trafficcontrol"
)

// task tells the shared traffic task handlers in internal/controlplane/trafficapi
// which TrafficBinding mode this API owns and how to name itself to clients.
var task = trafficapi.Task{
	Name:      "Mirror",
	Mode:      trafficv1alpha1.TrafficBindingModeMirror,
	ClaimMode: trafficcontrol.ModeMirror,
}
