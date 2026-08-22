package kubeapi

import (
	"encoding/json"

	"github.com/labstack/echo/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	kubernetesclient "k8s.io/client-go/kubernetes"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func (handler *Service) version(
	ctx *echo.Context,
	client kubernetesclient.Interface,
) *controlplaneapi.Error {
	request := ctx.Request()
	var result version.Info
	contents, err := client.Discovery().
		RESTClient().
		Get().
		AbsPath("/version").
		Do(request.Context()).
		Raw()
	if err != nil {
		return mapKubernetesError(err)
	}
	if err := json.Unmarshal(contents, &result); err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInternal,
			Message: "Kubernetes operation failed",
			Cause:   err,
		}
	}
	writeJSON(
		ctx,
		versionDocument{
			GitVersion:     result.GitVersion,
			GatewayVersion: handler.gatewayVersion,
		},
	)
	return nil
}

func (handler *Service) namespaces(
	ctx *echo.Context,
	client kubernetesclient.Interface,
) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, listErr := client.CoreV1().
		Namespaces().
		List(request.Context(), options)
	if listErr != nil {
		return mapKubernetesError(listErr)
	}
	items := make([]namespaceDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(
			items,
			namespaceDocument{
				Name:   result.Items[index].Name,
				Status: string(result.Items[index].Status.Phase),
			},
		)
	}
	writeJSON(
		ctx,
		listDocument[namespaceDocument]{
			Items:           items,
			Continue:        result.Continue,
			ResourceVersion: result.ResourceVersion,
		},
	)
	return nil
}

func (handler *Service) namespace(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	name string,
) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().
		Namespaces().
		Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(
		ctx,
		namespaceDocument{
			Name:   result.Name,
			Status: string(result.Status.Phase),
		},
	)
	return nil
}

func (handler *Service) pods(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, err := client.CoreV1().
		Pods(namespace).
		List(request.Context(), options)
	if err != nil {
		return mapKubernetesError(err)
	}
	items := make([]podDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(items, podFromKubernetes(&result.Items[index]))
	}
	writeJSON(
		ctx,
		listDocument[podDocument]{
			Items:           items,
			Continue:        result.Continue,
			ResourceVersion: result.ResourceVersion,
		},
	)
	return nil
}

func (handler *Service) pod(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	namespace, name string,
) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().
		Pods(namespace).
		Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(ctx, podFromKubernetes(result))
	return nil
}

func (handler *Service) services(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	namespace string,
) *controlplaneapi.Error {
	request := ctx.Request()
	options, apiError := listOptions(request)
	if apiError != nil {
		return apiError
	}
	result, err := client.CoreV1().
		Services(namespace).
		List(request.Context(), options)
	if err != nil {
		return mapKubernetesError(err)
	}
	items := make([]serviceDocument, 0, len(result.Items))
	for index := range result.Items {
		items = append(items, serviceFromKubernetes(&result.Items[index]))
	}
	writeJSON(
		ctx,
		listDocument[serviceDocument]{
			Items:           items,
			Continue:        result.Continue,
			ResourceVersion: result.ResourceVersion,
		},
	)
	return nil
}

func (handler *Service) service(
	ctx *echo.Context,
	client kubernetesclient.Interface,
	namespace, name string,
) *controlplaneapi.Error {
	request := ctx.Request()
	result, err := client.CoreV1().
		Services(namespace).
		Get(request.Context(), name, metav1.GetOptions{})
	if err != nil {
		return mapKubernetesError(err)
	}
	writeJSON(ctx, serviceFromKubernetes(result))
	return nil
}
