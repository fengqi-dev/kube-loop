package servicemodel

type Port struct {
	Name        string `json:"name,omitempty"`
	ServicePort int32  `json:"servicePort"`
	Protocol    string `json:"protocol"`
}

// LocalTarget captures the desktop-local endpoint mapping for one service port.
// It is persisted in the task spec so an active session can be restored after a
// desktop reconnect.
type LocalTarget struct {
	Protocol    string `json:"protocol"`
	ServicePort int32  `json:"servicePort"`
	LocalHost   string `json:"localHost,omitempty"`
	LocalPort   uint16 `json:"localPort"`
}

type ResolvedService struct {
	Name      string `json:"name"`
	ClusterIP string `json:"clusterIp"`
	Ports     []Port `json:"ports"`
}
