package discovery

import (
	"context"
	"net/netip"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var serviceCIDRErrorMarkers = []string{
	"the range of valid ips is",
	"the range of valid ips",
	"valid ip range is",
	"valid ips is",
}

// probeServiceCIDRs follows kubevpn's API-validation strategy. The invalid
// ClusterIPs are submitted as server-side dry runs, so successful discovery
// requires create permission but never persists a Service.
func probeServiceCIDRs(
	ctx context.Context,
	client kubernetes.Interface,
	namespace string,
) map[string]struct{} {
	result := make(map[string]struct{})
	if namespace == "" {
		namespace = "default"
	}
	for _, invalidIP := range []string{"0.0.0.0", "::"} {
		service := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "kubeloop-cidr-probe-"},
			Spec: corev1.ServiceSpec{
				ClusterIP: invalidIP,
				Ports:     []corev1.ServicePort{{Port: 80}},
			},
		}
		_, err := client.CoreV1().Services(namespace).Create(
			ctx, service, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}},
		)
		if err != nil {
			mergeCIDRs(result, serviceCIDRsFromError(err.Error()))
		}
	}
	return result
}

func serviceCIDRsFromError(message string) map[string]struct{} {
	result := make(map[string]struct{})
	lower := strings.ToLower(message)
	for _, marker := range serviceCIDRErrorMarkers {
		index := strings.LastIndex(lower, marker)
		if index < 0 {
			continue
		}
		remainder := message[index+len(marker):]
		for token := range strings.FieldsSeq(remainder) {
			token = strings.Trim(token, " \t\r\n,;()[]{}\"'")
			if prefix, err := netip.ParsePrefix(token); err == nil {
				result[prefix.Masked().String()] = struct{}{}
			}
		}
		break
	}
	return result
}
