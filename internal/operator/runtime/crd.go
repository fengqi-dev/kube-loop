package runtime

import (
	"context"
	"errors"
	"fmt"
	"os"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/yaml"
)

const trafficBindingCRDName = "trafficbindings.traffic.kubeloop.io"

func syncTrafficBindingCRDFile(
	ctx context.Context,
	config *rest.Config,
	path string,
) error {
	if path == "" {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read TrafficBinding CRD: %w", err)
	}
	client, err := apiextensionsclient.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("create CRD client: %w", err)
	}
	return syncTrafficBindingCRD(ctx, client, raw)
}

func syncTrafficBindingCRD(
	ctx context.Context,
	client apiextensionsclient.Interface,
	raw []byte,
) error {
	var desired apiextensionsv1.CustomResourceDefinition
	if err := yaml.Unmarshal(raw, &desired); err != nil {
		return fmt.Errorf("decode TrafficBinding CRD: %w", err)
	}
	if desired.Name != trafficBindingCRDName {
		return fmt.Errorf("unexpected CRD name %q", desired.Name)
	}
	crds := client.ApiextensionsV1().CustomResourceDefinitions()
	current, err := crds.Get(ctx, trafficBindingCRDName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return errors.New("TrafficBinding CRD is not installed")
		}
		return fmt.Errorf("get TrafficBinding CRD: %w", err)
	}
	if apiequality.Semantic.DeepEqual(current.Spec, desired.Spec) {
		return nil
	}
	updated := current.DeepCopy()
	updated.Spec = desired.Spec
	if _, err := crds.Update(ctx, updated, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("update TrafficBinding CRD: %w", err)
	}
	return nil
}
