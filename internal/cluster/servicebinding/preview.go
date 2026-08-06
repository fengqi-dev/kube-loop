package servicebinding

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	annotationPreviewID = "kubeloop.dev/preview-id"
	previewAppLabel     = "kubeloop-preview"
)

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// PreviewServiceSnapshot describes a managed ClusterIP Service that exposes a local process.
type PreviewServiceSnapshot struct {
	Namespace string          `json:"namespace"`
	Service   string          `json:"service"`
	Ports     []InterceptPort `json:"ports"`
	GatewayIP string          `json:"gatewayIP"`
}

// CreatePreviewService creates a managed Service and EndpointSlice for a local preview.
func CreatePreviewService(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	return createPreviewService(ctx, client, snapshot, previewID)
}

// DeletePreviewService removes resources created by CreatePreviewService.
func DeletePreviewService(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot PreviewServiceSnapshot,
) error {
	return deletePreviewService(ctx, client, snapshot)
}

func createPreviewService(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot PreviewServiceSnapshot,
	previewID string,
) (*corev1.Service, error) {
	if err := validatePreviewSnapshot(snapshot); err != nil {
		return nil, err
	}

	ports := make([]corev1.ServicePort, 0, len(snapshot.Ports))
	for _, port := range snapshot.Ports {
		protocol := port.Protocol
		if protocol == "" {
			protocol = corev1.ProtocolTCP
		}
		name := port.Name
		if name == "" {
			name = fmt.Sprintf("%s-%d", strings.ToLower(string(protocol)), port.ServicePort)
		}
		ports = append(ports, corev1.ServicePort{
			Name:       name,
			Port:       port.ServicePort,
			TargetPort: intstr.FromInt32(port.ListenPort),
			Protocol:   protocol,
		})
	}

	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      snapshot.Service,
			Namespace: snapshot.Namespace,
			Labels: map[string]string{
				interceptManagedLabel:    interceptManagedValue,
				"app.kubernetes.io/name": previewAppLabel,
			},
			Annotations: map[string]string{
				annotationPreviewID: previewID,
			},
		},
		Spec: corev1.ServiceSpec{
			Type:  corev1.ServiceTypeClusterIP,
			Ports: ports,
		},
	}

	created, err := client.CoreV1().Services(snapshot.Namespace).Create(ctx, desired, metav1.CreateOptions{})
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("service %s/%s already exists", snapshot.Namespace, snapshot.Service)
		}
		return nil, fmt.Errorf("create preview service: %w", err)
	}

	interceptSnapshot := ServiceInterceptSnapshot{
		Namespace: snapshot.Namespace,
		Service:   snapshot.Service,
		Ports:     snapshot.Ports,
		GatewayIP: snapshot.GatewayIP,
	}
	slice := managedEndpointSlice(interceptSnapshot, previewID)
	if slice.Labels == nil {
		slice.Labels = map[string]string{}
	}
	slice.Labels["app.kubernetes.io/name"] = previewAppLabel
	if slice.Annotations == nil {
		slice.Annotations = map[string]string{}
	}
	slice.Annotations[annotationPreviewID] = previewID
	delete(slice.Annotations, annotationInterceptID)

	_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(ctx, slice, metav1.CreateOptions{})
	if apierrors.IsAlreadyExists(err) {
		_ = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
			ctx, slice.Name, metav1.DeleteOptions{},
		)
		_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(ctx, slice, metav1.CreateOptions{})
	}
	if err != nil {
		_ = client.CoreV1().Services(snapshot.Namespace).Delete(ctx, snapshot.Service, metav1.DeleteOptions{})
		return nil, fmt.Errorf("create preview endpoint slice: %w", err)
	}
	return created, nil
}

func deletePreviewService(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot PreviewServiceSnapshot,
) error {
	_ = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
		ctx, managedEndpointSliceName(snapshot.Service), metav1.DeleteOptions{},
	)
	err := client.CoreV1().Services(snapshot.Namespace).Delete(
		ctx, snapshot.Service, metav1.DeleteOptions{},
	)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete preview service: %w", err)
	}
	return nil
}

func validatePreviewSnapshot(snapshot PreviewServiceSnapshot) error {
	if snapshot.Namespace == "" || snapshot.Service == "" {
		return fmt.Errorf("namespace and service are required")
	}
	if !dns1123Label.MatchString(snapshot.Service) || len(snapshot.Service) > 63 {
		return fmt.Errorf("service name %q is not a valid DNS-1123 label", snapshot.Service)
	}
	if snapshot.GatewayIP == "" {
		return fmt.Errorf("gateway IP is required")
	}
	if len(snapshot.Ports) == 0 {
		return fmt.Errorf("at least one port mapping is required")
	}
	for _, port := range snapshot.Ports {
		if port.ServicePort <= 0 || port.ServicePort > 65535 {
			return fmt.Errorf("invalid service port %d", port.ServicePort)
		}
		if port.ListenPort <= 0 || port.ListenPort > 65535 {
			return fmt.Errorf("invalid listen port %d", port.ListenPort)
		}
	}
	return nil
}
