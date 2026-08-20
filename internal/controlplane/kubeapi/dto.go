package kubeapi

type listDocument[T any] struct {
	Items           []T    `json:"items"`
	Continue        string `json:"continue,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
}
type versionDocument struct {
	GitVersion     string `json:"gitVersion"`
	GatewayVersion string `json:"gatewayVersion"`
}
type namespaceDocument struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}
type podDocument struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Phase           string            `json:"phase,omitempty"`
	PodIP           string            `json:"podIp,omitempty"`
	NodeName        string            `json:"nodeName,omitempty"`
	Ready           bool              `json:"ready"`
	ReadyContainers int32             `json:"readyContainers"`
	TotalContainers int32             `json:"totalContainers"`
	Restarts        int32             `json:"restarts"`
	AgeSeconds      int64             `json:"ageSeconds"`
	Containers      []string          `json:"containers"`
	Ports           []podPortDocument `json:"ports"`
}
type podPortDocument struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}
type serviceDocument struct {
	Name         string                `json:"name"`
	Namespace    string                `json:"namespace"`
	Type         string                `json:"type"`
	ClusterIP    string                `json:"clusterIp,omitempty"`
	ExternalName string                `json:"externalName,omitempty"`
	ExternalIPs  []string              `json:"externalIps"`
	AgeSeconds   int64                 `json:"ageSeconds"`
	Ports        []servicePortDocument `json:"ports"`
}
type servicePortDocument struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort string `json:"targetPort,omitempty"`
}
