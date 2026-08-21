package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const (
	ConditionReady    = "Ready"
	ConditionDegraded = "Degraded"
)

// TrafficBindingMode identifies the traffic workflow represented by a binding.
// +kubebuilder:validation:Enum=PortForward;Preview;Exchange;Mirror
type TrafficBindingMode string

const (
	TrafficBindingModePortForward TrafficBindingMode = "PortForward"
	TrafficBindingModePreview     TrafficBindingMode = "Preview"
	TrafficBindingModeExchange    TrafficBindingMode = "Exchange"
	TrafficBindingModeMirror      TrafficBindingMode = "Mirror"
)

// TargetKind is a Kubernetes resource that can receive a traffic workflow.
// +kubebuilder:validation:Enum=Pod;Service
type TargetKind string

const (
	TargetKindPod     TargetKind = "Pod"
	TargetKindService TargetKind = "Service"
)

// TransportProtocol is the network protocol exposed by one binding.
// +kubebuilder:validation:Enum=TCP;UDP
type TransportProtocol string

const (
	TransportProtocolTCP TransportProtocol = "TCP"
	TransportProtocolUDP TransportProtocol = "UDP"
)

// TrafficTarget references an existing Pod or Service in the binding namespace.
type TrafficTarget struct {
	// kind selects the Kubernetes target type.
	// +required
	Kind TargetKind `json:"kind"`

	// name is the DNS-1123 name of the target object.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +required
	Name string `json:"name"`
}

// RelayEndpoint is a Gateway-owned listener reachable from the cluster.
// It contains no user credential or local desktop address.
type RelayEndpoint struct {
	// address must be one IPv4 or IPv6 literal.
	// +kubebuilder:validation:MinLength=2
	// +kubebuilder:validation:MaxLength=45
	// +required
	Address string `json:"address"`
}

// PreviewExposure describes the Service created for Preview mode.
type PreviewExposure struct {
	// serviceName is the create-only ClusterIP Service name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +required
	ServiceName string `json:"serviceName"`
}

// TrafficPort maps one Kubernetes target or Service port to an optional
// Gateway relay listener.
type TrafficPort struct {
	// name is the Service port name. The Operator derives a stable name when omitted.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Name string `json:"name,omitempty"`

	// targetPort is the Pod/Service destination port, or the Service port created for Preview.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +required
	TargetPort int32 `json:"targetPort"`

	// relayPort is required for Preview, Exchange and Mirror and forbidden for PortForward.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	RelayPort *int32 `json:"relayPort,omitempty"`

	// protocol is TCP or UDP.
	// +required
	Protocol TransportProtocol `json:"protocol"`
}

// TrafficBindingSpec defines the immutable desired state of TrafficBinding.
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="TrafficBinding spec is immutable"
// +kubebuilder:validation:XValidation:rule="self.mode != 'PortForward' || (has(self.target) && !has(self.relay) && !has(self.preview) && size(self.ports) == 1 && self.ports.all(p, !has(p.relayPort)))",message="PortForward requires one target port and forbids relay ports and preview"
// +kubebuilder:validation:XValidation:rule="self.mode != 'Preview' || (!has(self.target) && has(self.relay) && has(self.preview) && self.ports.all(p, has(p.relayPort)))",message="Preview requires relay, preview and relayPort on every port"
// +kubebuilder:validation:XValidation:rule="!(self.mode in ['Exchange', 'Mirror']) || (has(self.target) && self.target.kind == 'Service' && has(self.relay) && !has(self.preview) && self.ports.all(p, has(p.relayPort)))",message="Exchange and Mirror require a Service target, relay and relayPort on every port"
type TrafficBindingSpec struct {
	// mode selects the workflow semantics.
	// +required
	Mode TrafficBindingMode `json:"mode"`

	// sessionID is the owning KubeLoop Cluster Session UUID.
	// +kubebuilder:validation:Format=uuid
	// +required
	SessionID string `json:"sessionID"`

	// taskID is the owning KubeLoop Task UUID and is unique per binding.
	// +kubebuilder:validation:Format=uuid
	// +required
	TaskID string `json:"taskID"`

	// sessionGeneration prevents an old binding from being reused by a newer
	// incarnation of the same Session.
	// +kubebuilder:validation:Minimum=1
	// +required
	SessionGeneration int64 `json:"sessionGeneration"`

	// target is required by PortForward, Exchange and Mirror.
	// +optional
	Target *TrafficTarget `json:"target,omitempty"`

	// relay is required by Preview, Exchange and Mirror. It is a trusted
	// Gateway listener, never a desktop-provided destination.
	// +optional
	Relay *RelayEndpoint `json:"relay,omitempty"`

	// preview is required only by Preview mode.
	// +optional
	Preview *PreviewExposure `json:"preview,omitempty"`

	// ports contains one PortForward port or up to 64 Preview/Exchange/Mirror mappings.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=64
	// +listType=map
	// +listMapKey=protocol
	// +listMapKey=targetPort
	// +required
	Ports []TrafficPort `json:"ports"`
}

// EndpointSliceSnapshot is the exact endpoint state captured before an
// Exchange or Mirror takes ownership of a Service.
type EndpointSliceSnapshot struct {
	Name            string                     `json:"name"`
	AddressType     discoveryv1.AddressType    `json:"addressType"`
	Labels          map[string]string          `json:"labels,omitempty"`
	Annotations     map[string]string          `json:"annotations,omitempty"`
	OwnerReferences []metav1.OwnerReference    `json:"ownerReferences,omitempty"`
	Endpoints       []discoveryv1.Endpoint     `json:"endpoints,omitempty"`
	Ports           []discoveryv1.EndpointPort `json:"ports,omitempty"`
}

// ServiceSnapshot is durable rollback state captured before Service mutation.
type ServiceSnapshot struct {
	ServiceName       string                  `json:"serviceName"`
	ServiceUID        types.UID               `json:"serviceUID"`
	Selector          map[string]string       `json:"selector,omitempty"`
	EndpointSlices    []EndpointSliceSnapshot `json:"endpointSlices,omitempty"`
	HadEndpointSlices bool                    `json:"hadEndpointSlices,omitempty"`
	EndpointSubsets   []corev1.EndpointSubset `json:"endpointSubsets,omitempty"`
	HadEndpoints      bool                    `json:"hadEndpoints,omitempty"`
}

// TrafficBindingPhase summarizes the reconciliation lifecycle.
// +kubebuilder:validation:Enum=Pending;Ready;Degraded;Restoring
type TrafficBindingPhase string

const (
	TrafficBindingPhasePending   TrafficBindingPhase = "Pending"
	TrafficBindingPhaseReady     TrafficBindingPhase = "Ready"
	TrafficBindingPhaseDegraded  TrafficBindingPhase = "Degraded"
	TrafficBindingPhaseRestoring TrafficBindingPhase = "Restoring"
)

// TrafficBindingStatus defines the observed state of TrafficBinding.
type TrafficBindingStatus struct {
	// observedGeneration is the metadata generation last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// phase is a concise lifecycle summary; conditions carry authoritative detail.
	// +optional
	Phase TrafficBindingPhase `json:"phase,omitempty"`

	// serviceName is the affected or created Service, when applicable.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// serviceClusterIP is populated for a ready Preview.
	// +optional
	ServiceClusterIP string `json:"serviceClusterIP,omitempty"`

	// snapshot is captured and persisted before Exchange or Mirror mutation.
	// +optional
	Snapshot *ServiceSnapshot `json:"snapshot,omitempty"`

	// conditions use Ready and Degraded as stable condition types.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=tb
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Service",type=string,JSONPath=".status.serviceName"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// TrafficBinding is the schema for the trafficbindings API.
type TrafficBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec TrafficBindingSpec `json:"spec"`

	// +optional
	Status TrafficBindingStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TrafficBindingList contains a list of TrafficBinding.
type TrafficBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`

	Items []TrafficBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &TrafficBinding{}, &TrafficBindingList{})
		return nil
	})
}
