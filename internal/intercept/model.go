package intercept

import "context"

const (
	ProtocolTCP = "TCP"
	ProtocolUDP = "UDP"
)

// Service is the application-facing view of a cluster Service. Infrastructure
// adapters translate provider-specific service objects into this model.
type Service struct {
	Namespace string
	Name      string
	ClusterIP string
	Selector  map[string]string
	Ports     []ServicePort
}

type ServicePort struct {
	Name     string
	Protocol string
	Port     int32
}

// Backend is a ready workload address and the ports exposed by that endpoint.
type Backend struct {
	Address string
	Ports   []BackendPort
}

type BackendPort struct {
	Name     string
	Protocol string
	Port     int32
}

// InterceptPort maps one Service port onto a unique Gateway listen port.
type InterceptPort struct {
	Name        string `json:"name"`
	Protocol    string `json:"protocol"`
	ServicePort int32  `json:"servicePort"`
	ListenPort  int32  `json:"listenPort"`
}

type ServiceInterceptRequest struct {
	Namespace string
	Service   string
	Selector  map[string]string
	Ports     []InterceptPort
	GatewayIP string
	ID        string
}

type PreviewServiceRequest struct {
	Namespace string
	Service   string
	Ports     []InterceptPort
	GatewayIP string
	ID        string
}

// Lease owns infrastructure state created for an intercept or preview.
type Lease interface {
	Release(context.Context) error
}

type ClusterAPI interface {
	GetService(context.Context, string, string, string) (*Service, error)
	ApplyServiceIntercept(
		context.Context, string, ServiceInterceptRequest,
	) (Lease, []Backend, error)
	CreatePreviewService(
		context.Context, string, PreviewServiceRequest,
	) (*Service, Lease, error)
}
