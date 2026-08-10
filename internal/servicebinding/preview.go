package servicebinding

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
)

const (
	annotationPreviewID = "kubeloop.dev/preview-id"
	previewOwnerLabel   = "kubeloop.dev/preview-owner"
	previewAppLabel     = "kubeloop-preview"
)

var dns1123Label = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// ErrPreviewCleanupPending means create failed and its exact-owner
// compensation also failed. Callers must retain durable cleanup intent.
var ErrPreviewCleanupPending = errors.New("preview resource cleanup is pending")

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
	previewID string,
) error {
	return deletePreviewService(ctx, client, snapshot, previewID)
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
	previewID = strings.TrimSpace(previewID)
	if previewID == "" || len(previewID) > 256 {
		return nil, fmt.Errorf("preview owner ID is required and must not exceed 256 characters")
	}
	ownerLabel := previewOwnerLabelValue(previewID)

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
				previewOwnerLabel:        ownerLabel,
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
	slice.Labels[previewOwnerLabel] = ownerLabel
	if slice.Annotations == nil {
		slice.Annotations = map[string]string{}
	}
	slice.Annotations[annotationPreviewID] = previewID
	delete(slice.Annotations, annotationInterceptID)

	_, err = client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Create(ctx, slice, metav1.CreateOptions{})
	if err != nil {
		cleanupErr := deleteOwnedPreviewServiceObject(ctx, client, created, previewID)
		if apierrors.IsAlreadyExists(err) {
			err = fmt.Errorf("endpoint slice %s/%s already exists", snapshot.Namespace, slice.Name)
		} else {
			err = fmt.Errorf("create preview endpoint slice: %w", err)
		}
		if cleanupErr != nil {
			return nil, errors.Join(err, fmt.Errorf("%w: %v", ErrPreviewCleanupPending, cleanupErr))
		}
		return nil, err
	}
	return created, nil
}

func deletePreviewService(
	ctx context.Context,
	client kubernetes.Interface,
	snapshot PreviewServiceSnapshot,
	previewID string,
) error {
	if err := validatePreviewSnapshot(snapshot); err != nil {
		return err
	}
	previewID = strings.TrimSpace(previewID)
	if previewID == "" || len(previewID) > 256 {
		return fmt.Errorf("preview owner ID is required and must not exceed 256 characters")
	}

	var result error
	slice, err := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Get(
		ctx, managedEndpointSliceName(snapshot.Service), metav1.GetOptions{},
	)
	if err == nil {
		if previewOwned(slice.Labels, slice.Annotations, previewID) {
			if deleteErr := client.DiscoveryV1().EndpointSlices(snapshot.Namespace).Delete(
				ctx, slice.Name, deleteOptionsForUID(slice.UID),
			); deleteErr != nil && !apierrors.IsNotFound(deleteErr) {
				result = errors.Join(result, fmt.Errorf("delete owned preview endpoint slice: %w", deleteErr))
			}
		}
	} else if !apierrors.IsNotFound(err) {
		result = errors.Join(result, fmt.Errorf("get preview endpoint slice for ownership check: %w", err))
	}

	service, err := client.CoreV1().Services(snapshot.Namespace).Get(
		ctx, snapshot.Service, metav1.GetOptions{},
	)
	if err == nil {
		if previewOwned(service.Labels, service.Annotations, previewID) {
			if deleteErr := deleteOwnedPreviewServiceObject(ctx, client, service, previewID); deleteErr != nil {
				result = errors.Join(result, fmt.Errorf("delete owned preview service: %w", deleteErr))
			}
		}
	} else if !apierrors.IsNotFound(err) {
		result = errors.Join(result, fmt.Errorf("get preview service for ownership check: %w", err))
	}
	return result
}

func deleteOwnedPreviewServiceObject(
	ctx context.Context,
	client kubernetes.Interface,
	service *corev1.Service,
	previewID string,
) error {
	if service == nil || !previewOwned(service.Labels, service.Annotations, previewID) {
		return fmt.Errorf("service is no longer owned by preview %s", previewID)
	}
	err := client.CoreV1().Services(service.Namespace).Delete(
		ctx, service.Name, deleteOptionsForUID(service.UID),
	)
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

func previewOwned(labels, annotations map[string]string, previewID string) bool {
	return labels[interceptManagedLabel] == interceptManagedValue &&
		labels["app.kubernetes.io/name"] == previewAppLabel &&
		labels[previewOwnerLabel] == previewOwnerLabelValue(previewID) &&
		annotations[annotationPreviewID] == previewID
}

func previewOwnerLabelValue(previewID string) string {
	digest := sha256.Sum256([]byte(previewID))
	return fmt.Sprintf("%x", digest[:16])
}

func deleteOptionsForUID(uid types.UID) metav1.DeleteOptions {
	if uid == "" {
		return metav1.DeleteOptions{}
	}
	return metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}}
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
