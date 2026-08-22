package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	trafficv1alpha1 "github.com/fengqi-dev/kube-loop/api/v1alpha1"
)

func applyBindingMetadata(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) {
	labels := maps.Clone(object.GetLabels())
	if labels == nil {
		labels = map[string]string{}
	}
	labels[managedByLabel] = managedByValue
	labels[bindingNameLabel] = bindingLabelValue(binding.UID)
	object.SetLabels(labels)
	applyBindingAnnotations(object, binding)
}

func applyBindingAnnotations(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) {
	annotations := maps.Clone(object.GetAnnotations())
	if annotations == nil {
		annotations = map[string]string{}
	}
	annotations[bindingNameAnnotation] = binding.Name
	annotations[bindingUIDAnnotation] = string(binding.UID)
	annotations[bindingModeAnnotation] = string(binding.Spec.Mode)
	object.SetAnnotations(annotations)
}

func removeBindingMetadata(object metav1.Object) {
	labels := maps.Clone(object.GetLabels())
	delete(labels, bindingNameLabel)
	object.SetLabels(labels)
	annotations := maps.Clone(object.GetAnnotations())
	delete(annotations, bindingNameAnnotation)
	delete(annotations, bindingUIDAnnotation)
	delete(annotations, bindingModeAnnotation)
	object.SetAnnotations(annotations)
}

func ownedByBinding(object metav1.Object, binding *trafficv1alpha1.TrafficBinding) bool {
	return object.GetAnnotations()[bindingNameAnnotation] == binding.Name &&
		object.GetAnnotations()[bindingUIDAnnotation] == string(binding.UID)
}

func bindingLabelValue(uid types.UID) string {
	digest := sha256.Sum256([]byte(uid))
	return hex.EncodeToString(digest[:16])
}

func managedEndpointSliceName(binding *trafficv1alpha1.TrafficBinding) string {
	return "kubeloop-" + bindingLabelValue(binding.UID)
}

func cloneDiscoveryEndpoints(source []discoveryv1.Endpoint) []discoveryv1.Endpoint {
	if len(source) == 0 {
		return nil
	}
	result := make([]discoveryv1.Endpoint, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func cloneDiscoveryPorts(source []discoveryv1.EndpointPort) []discoveryv1.EndpointPort {
	if len(source) == 0 {
		return nil
	}
	result := make([]discoveryv1.EndpointPort, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func cloneEndpointSubsets(source []corev1.EndpointSubset) []corev1.EndpointSubset {
	if len(source) == 0 {
		return nil
	}
	result := make([]corev1.EndpointSubset, len(source))
	for index := range source {
		result[index] = *source[index].DeepCopy()
	}
	return result
}

func errorsJoin(left, right error) error {
	if left == nil {
		return right
	}
	return fmt.Errorf("%w; %w", left, right)
}
