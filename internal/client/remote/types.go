package remote

import (
	"fmt"

	"github.com/fengqi-dev/kube-loop/internal/protocol/capability"
)

type APIError struct {
	Status    int
	Code      string
	Message   string
	Field     string
	RequestID string
}

func (apiError *APIError) Error() string {
	if apiError == nil {
		return ""
	}
	if apiError.Code != "" {
		return fmt.Sprintf("Gateway request failed (%s): %s", apiError.Code, apiError.Message)
	}
	return fmt.Sprintf("Gateway request returned HTTP %d", apiError.Status)
}

type Version struct {
	GitVersion     string `json:"gitVersion"`
	GatewayVersion string `json:"gatewayVersion"`
}

type Capabilities = capability.Snapshot

type page[T any] struct {
	Items           []T    `json:"items"`
	Continue        string `json:"continue,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}
