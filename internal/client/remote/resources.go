package remote

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/fengqi-dev/kube-loop/internal/client/profile"
)

func (client *Client) Namespaces(ctx context.Context, serverProfile profile.Profile) ([]Namespace, error) {
	return collectPages[Namespace](ctx, client, serverProfile, "/api/namespaces")
}

func (client *Client) Pods(ctx context.Context, serverProfile profile.Profile, namespace string) ([]Pod, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	pods, err := collectPages[Pod](ctx, client, serverProfile, "/api/namespaces/"+url.PathEscape(namespace)+"/pods")
	if err != nil {
		return nil, err
	}
	for index := range pods {
		if pods[index].Containers == nil {
			pods[index].Containers = []string{}
		}
		if pods[index].Ports == nil {
			pods[index].Ports = []PodPort{}
		}
	}
	return pods, nil
}

func (client *Client) Services(
	ctx context.Context,
	serverProfile profile.Profile,
	namespace string,
) ([]Service, error) {
	if err := validateNamespace(namespace); err != nil {
		return nil, err
	}
	return collectPages[Service](ctx, client, serverProfile, "/api/namespaces/"+url.PathEscape(namespace)+"/services")
}

func collectPages[T any](
	ctx context.Context,
	client *Client,
	serverProfile profile.Profile,
	requestPath string,
) ([]T, error) {
	items := make([]T, 0)
	continueToken := ""
	for range maximumPages {
		query := url.Values{"limit": {strconv.Itoa(pageLimit)}}
		if continueToken != "" {
			query.Set("continue", continueToken)
		}
		var result page[T]
		if err := client.getJSON(ctx, serverProfile, requestPath, query, &result); err != nil {
			return nil, err
		}
		if result.Items == nil {
			result.Items = []T{}
		}
		items = append(items, result.Items...)
		if result.Continue == "" {
			return items, nil
		}
		if result.Continue == continueToken {
			return nil, errors.New("gateway returned a repeated pagination token")
		}
		continueToken = result.Continue
	}
	return nil, errors.New("gateway inventory exceeds the pagination safety limit")
}
