package discovery

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type Result struct {
	PodCIDRs     []string
	ServiceCIDRs []string
	ServiceIPs   []string
	DNSServer    string
	Pods         int
	Services     int
	Deployments  int
}

// Discover collects routable CIDRs and live resource counts. Node,
// deployment, ServiceCIDR, and kube-system reads are best-effort so
// namespace-scoped users can still connect.
func Discover(
	ctx context.Context,
	client kubernetes.Interface,
	namespaces []string,
) (Result, error) {
	if client == nil {
		return Result{}, fmt.Errorf("kubernetes client is required")
	}
	podCIDRs := make(map[string]struct{})
	if nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err == nil {
		podCIDRs = collectNodePodCIDRs(nodes.Items)
	}

	var pods []corev1.Pod
	var services []corev1.Service
	if len(namespaces) == 0 {
		podList, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("list pods: %w", err)
		}
		pods = podList.Items
		serviceList, err := client.CoreV1().Services("").List(ctx, metav1.ListOptions{})
		if err != nil {
			return Result{}, fmt.Errorf("list services: %w", err)
		}
		services = serviceList.Items
	} else {
		for _, namespace := range namespaces {
			podList, err := client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				return Result{}, fmt.Errorf("list pods in %s: %w", namespace, err)
			}
			pods = append(pods, podList.Items...)
			serviceList, err := client.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				return Result{}, fmt.Errorf("list services in %s: %w", namespace, err)
			}
			services = append(services, serviceList.Items...)
		}
		if service, err := client.CoreV1().Services("kube-system").Get(
			ctx, "kube-dns", metav1.GetOptions{},
		); err == nil {
			services = append(services, *service)
		} else if service, err := client.CoreV1().Services("kube-system").Get(
			ctx, "coredns", metav1.GetOptions{},
		); err == nil {
			services = append(services, *service)
		}
	}

	serviceIPs := make(map[string]struct{})
	dnsServer := ""
	for _, service := range services {
		for _, raw := range service.Spec.ClusterIPs {
			if ip, err := netip.ParseAddr(raw); err == nil {
				serviceIPs[ip.String()] = struct{}{}
				if service.Namespace == "kube-system" &&
					(service.Name == "kube-dns" || service.Name == "coredns") {
					dnsServer = ip.String()
				}
			}
		}
	}

	deployments := 0
	if list, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{}); err == nil {
		deployments = len(list.Items)
	}
	return Result{
		PodCIDRs:     sortedKeys(podCIDRs),
		ServiceCIDRs: discoverServiceCIDRs(ctx, client),
		ServiceIPs:   sortedKeys(serviceIPs),
		DNSServer:    dnsServer,
		Pods:         len(pods),
		Services:     len(services),
		Deployments:  deployments,
	}, nil
}

func collectNodePodCIDRs(nodes []corev1.Node) map[string]struct{} {
	podCIDRs := make(map[string]struct{})
	for _, node := range nodes {
		for _, cidr := range node.Spec.PodCIDRs {
			if prefix, err := netip.ParsePrefix(cidr); err == nil {
				podCIDRs[prefix.Masked().String()] = struct{}{}
			}
		}
		if node.Spec.PodCIDR != "" {
			if prefix, err := netip.ParsePrefix(node.Spec.PodCIDR); err == nil {
				podCIDRs[prefix.Masked().String()] = struct{}{}
			}
		}
	}
	return podCIDRs
}

func discoverServiceCIDRs(ctx context.Context, client kubernetes.Interface) []string {
	cidrs := make(map[string]struct{})
	if list, err := client.NetworkingV1().ServiceCIDRs().List(ctx, metav1.ListOptions{}); err == nil {
		for _, item := range list.Items {
			for _, raw := range item.Spec.CIDRs {
				if prefix, err := netip.ParsePrefix(raw); err == nil {
					cidrs[prefix.Masked().String()] = struct{}{}
				}
			}
		}
	}
	if len(cidrs) == 0 {
		if subnet, err := serviceSubnetFromKubeadm(ctx, client); err == nil && subnet != "" {
			if prefix, err := netip.ParsePrefix(subnet); err == nil {
				cidrs[prefix.Masked().String()] = struct{}{}
			}
		}
	}
	return sortedKeys(cidrs)
}

func serviceSubnetFromKubeadm(ctx context.Context, client kubernetes.Interface) (string, error) {
	configMap, err := client.CoreV1().ConfigMaps("kube-system").Get(
		ctx, "kubeadm-config", metav1.GetOptions{},
	)
	if err != nil {
		return "", err
	}
	raw, ok := configMap.Data["ClusterConfiguration"]
	if !ok || strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("kubeadm-config missing ClusterConfiguration")
	}
	var parsed struct {
		Networking struct {
			ServiceSubnet string `yaml:"serviceSubnet"`
		} `yaml:"networking"`
	}
	if err := yaml.Unmarshal([]byte(raw), &parsed); err != nil {
		return "", err
	}
	return strings.TrimSpace(parsed.Networking.ServiceSubnet), nil
}

func sortedKeys(values map[string]struct{}) []string {
	items := slices.AppendSeq(make([]string, 0, len(values)), maps.Keys(values))
	slices.Sort(items)
	return items
}
