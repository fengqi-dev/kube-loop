package entity

type Port struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

type ResolvedService struct {
	Name      string `json:"name"`
	ClusterIP string `json:"clusterIp"`
	Ports     []Port `json:"ports"`
}
