package kubeapi

import (
	"fmt"
	"net/http"
	"strconv"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
)

func listOptions(
	request *http.Request,
) (metav1.ListOptions, *controlplaneapi.Error) {
	for key, values := range request.URL.Query() {
		if key != "limit" && key != "continue" &&
			key != labelSelectorQueryParameter &&
			key != fieldSelectorQueryParameter {
			return metav1.ListOptions{}, invalidQuery(key)
		}
		if len(values) != 1 {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	limit := defaultListLimit
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 1 || parsed > maximumListLimit {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: "limit",
				Message: fmt.Sprintf(
					"limit must be between 1 and %d",
					maximumListLimit,
				),
			}
		}
		limit = parsed
	}
	continueToken := request.URL.Query().Get("continue")
	if len(continueToken) > maximumContinue || containsControl(continueToken) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "continue", Message: "continue token is invalid",
		}
	}
	labelSelector := request.URL.Query().Get(labelSelectorQueryParameter)
	if len(labelSelector) > 1024 || containsControl(labelSelector) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   labelSelectorQueryParameter,
			Message: "label selector is invalid",
		}
	}
	if labelSelector != "" {
		if _, err := labels.Parse(labelSelector); err != nil {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   labelSelectorQueryParameter,
				Message: "label selector is invalid",
			}
		}
	}
	fieldSelector := request.URL.Query().Get(fieldSelectorQueryParameter)
	if len(fieldSelector) > 1024 || containsControl(fieldSelector) {
		return metav1.ListOptions{}, &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   fieldSelectorQueryParameter,
			Message: "field selector is invalid",
		}
	}
	if fieldSelector != "" {
		if _, err := fields.ParseSelector(fieldSelector); err != nil {
			return metav1.ListOptions{}, &controlplaneapi.Error{
				Code:    controlplaneapi.CodeInvalidArgument,
				Field:   fieldSelectorQueryParameter,
				Message: "field selector is invalid",
			}
		}
	}
	return metav1.ListOptions{
		Limit: limit, Continue: continueToken, LabelSelector: labelSelector, FieldSelector: fieldSelector,
	}, nil
}

func capabilityNamespace(
	request *http.Request,
) (string, *controlplaneapi.Error) {
	query := request.URL.Query()
	for key, values := range query {
		if key != "namespace" {
			return "", invalidQuery(key)
		}
		if len(values) != 1 {
			return "", &controlplaneapi.Error{
				Code: controlplaneapi.CodeInvalidArgument, Field: key, Message: "query parameter must be provided once",
			}
		}
	}
	namespace := query.Get("namespace")
	if namespace == "" {
		return "", &controlplaneapi.Error{
			Code: controlplaneapi.CodeInvalidArgument, Field: "namespace", Message: "namespace is required",
		}
	}
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return "", apiError
	}
	return namespace, nil
}

func rejectQuery(request *http.Request) *controlplaneapi.Error {
	for key := range request.URL.Query() {
		return invalidQuery(key)
	}
	return nil
}

func invalidQuery(field string) *controlplaneapi.Error {
	return &controlplaneapi.Error{
		Code:    controlplaneapi.CodeInvalidArgument,
		Field:   field,
		Message: "query parameter is not supported",
	}
}

func validateNames(namespace, name string) *controlplaneapi.Error {
	if apiError := validateName("namespace", namespace, true); apiError != nil {
		return apiError
	}
	return validateName("name", name, false)
}

func validateName(field, value string, namespace bool) *controlplaneapi.Error {
	var problems []string
	if namespace {
		problems = validation.IsDNS1123Label(value)
	} else {
		problems = validation.IsDNS1123Subdomain(value)
	}
	if len(problems) != 0 {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   field,
			Message: field + " is invalid",
		}
	}
	return nil
}
