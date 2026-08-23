package remote

type Namespace struct {
	Name   string `json:"name"`
	Status string `json:"status,omitempty"`
}

type Pod struct {
	Name            string    `json:"name"`
	Namespace       string    `json:"namespace"`
	Phase           string    `json:"phase,omitempty"`
	PodIP           string    `json:"podIp,omitempty"`
	NodeName        string    `json:"nodeName,omitempty"`
	Ready           bool      `json:"ready"`
	ReadyContainers int32     `json:"readyContainers"`
	TotalContainers int32     `json:"totalContainers"`
	Restarts        int32     `json:"restarts"`
	AgeSeconds      int64     `json:"ageSeconds"`
	Containers      []string  `json:"containers"`
	Ports           []PodPort `json:"ports"`
}

type PodPort struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol"`
}

type Service struct {
	Name         string        `json:"name"`
	Namespace    string        `json:"namespace"`
	Type         string        `json:"type"`
	ClusterIP    string        `json:"clusterIp,omitempty"`
	ExternalName string        `json:"externalName,omitempty"`
	ExternalIPs  []string      `json:"externalIps"`
	AgeSeconds   int64         `json:"ageSeconds"`
	Ports        []ServicePort `json:"ports"`
}

type ServicePort struct {
	Name       string `json:"name,omitempty"`
	Port       int32  `json:"port"`
	Protocol   string `json:"protocol"`
	TargetPort string `json:"targetPort,omitempty"`
}
