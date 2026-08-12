package servicebinding

import "errors"

// ErrPreviewCleanupPending means Preview activation failed after creating a
// TrafficBinding. The caller must retain durable cleanup intent.
var ErrPreviewCleanupPending = errors.New("preview resource cleanup is pending")

// PreviewServiceSnapshot describes the Service requested through a Preview
// TrafficBinding. The operator owns its lifecycle.
type PreviewServiceSnapshot struct {
	Namespace string          `json:"namespace"`
	Service   string          `json:"service"`
	Ports     []InterceptPort `json:"ports"`
	GatewayIP string          `json:"gatewayIP"`
}
