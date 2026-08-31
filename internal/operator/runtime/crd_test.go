package runtime

import (
	"context"
	"strconv"
	"strings"
	"testing"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsfake "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/fake"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSyncTrafficBindingCRDUpdatesSchemaAndPreservesMetadata(t *testing.T) {
	current := testTrafficBindingCRD("Pending", "Ready")
	current.Labels = map[string]string{"installed-by": "helm"}
	client := apiextensionsfake.NewSimpleClientset(current)
	desired := `
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: trafficbindings.traffic.kubeloop.io
spec:
  group: traffic.kubeloop.io
  names:
    kind: TrafficBinding
    plural: trafficbindings
  scope: Namespaced
  versions:
    - name: v1alpha1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            status:
              type: object
              properties:
                phase:
                  type: string
                  enum: [Pending, Reconciling, Ready, Restored]
`
	if err := syncTrafficBindingCRD(t.Context(), client, []byte(desired)); err != nil {
		t.Fatal(err)
	}
	updated, err := client.ApiextensionsV1().CustomResourceDefinitions().Get(
		t.Context(),
		trafficBindingCRDName,
		metav1.GetOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Labels["installed-by"] != "helm" {
		t.Fatalf("metadata was replaced: %#v", updated.Labels)
	}
	phase := updated.Spec.Versions[0].Schema.OpenAPIV3Schema.Properties["status"].
		Properties["phase"]
	if got := len(phase.Enum); got != 4 || string(phase.Enum[1].Raw) != `"Reconciling"` {
		t.Fatalf("phase enum = %#v", phase.Enum)
	}
}

func TestSyncTrafficBindingCRDRejectsUnexpectedManifest(t *testing.T) {
	client := apiextensionsfake.NewSimpleClientset(testTrafficBindingCRD())
	err := syncTrafficBindingCRD(
		context.Background(),
		client,
		[]byte("metadata:\n  name: other.example.com\n"),
	)
	if err == nil || !strings.Contains(err.Error(), "unexpected CRD name") {
		t.Fatalf("error = %v", err)
	}
}

func testTrafficBindingCRD(values ...string) *apiextensionsv1.CustomResourceDefinition {
	enum := make([]apiextensionsv1.JSON, 0, len(values))
	for _, value := range values {
		enum = append(enum, apiextensionsv1.JSON{Raw: []byte(strconv.Quote(value))})
	}
	return &apiextensionsv1.CustomResourceDefinition{
		Name: trafficBindingCRDName,
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: "traffic.kubeloop.io",
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind: "TrafficBinding", Plural: "trafficbindings",
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name: "v1alpha1", Served: true, Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
						Type: "object",
						Properties: map[string]apiextensionsv1.JSONSchemaProps{
							"status": {
								Type: "object",
								Properties: map[string]apiextensionsv1.JSONSchemaProps{
									"phase": {Type: "string", Enum: enum},
								},
							},
						},
					},
				},
			}},
		},
	}
}
