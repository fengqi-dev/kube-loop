package kubeapi

import (
	"errors"
	"slices"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/tools/cache"
)

func (feed *inventoryFeed) run() {
	if feed.readyErr == nil {
		feed.factory.Start(feed.stop)
		if !cache.WaitForCacheSync(feed.stop, feed.informer.HasSynced) {
			feed.readyErr = errors.New(
				"inventory Watch cache synchronization failed",
			)
		}
	}
	close(feed.ready)
	if feed.readyErr != nil {
		return
	}
	feed.schedule()
	for {
		select {
		case <-feed.stop:
			feed.factory.Shutdown()
			return
		case <-feed.dirty:
			feed.publish()
		}
	}
}

func (feed *inventoryFeed) changed(object any) {
	if accessor, err := apiMeta.Accessor(object); err == nil {
		feed.hub.mu.Lock()
		feed.version = accessor.GetResourceVersion()
		feed.hub.mu.Unlock()
	}
	feed.schedule()
}

func (feed *inventoryFeed) schedule() {
	select {
	case feed.dirty <- struct{}{}:
	default:
	}
}

func (feed *inventoryFeed) publish() {
	objects := feed.informer.GetStore().List()
	snapshot := inventorySnapshot{
		SchemaVersion: 1, Type: "snapshot", Resource: feed.key.Resource, Namespace: feed.key.Namespace,
		GeneratedAt: time.Now().UTC(),
	}
	if feed.key.Resource == inventoryPods {
		snapshot.Pods = make([]podDocument, 0, len(objects))
		for _, object := range objects {
			if pod, ok := object.(*corev1.Pod); ok {
				snapshot.Pods = append(snapshot.Pods, podFromKubernetes(pod))
			}
		}
		slices.SortFunc(
			snapshot.Pods,
			func(left, right podDocument) int { return strings.Compare(left.Name, right.Name) },
		)
	} else {
		snapshot.Services = make([]serviceDocument, 0, len(objects))
		for _, object := range objects {
			if service, ok := object.(*corev1.Service); ok {
				snapshot.Services = append(snapshot.Services, serviceFromKubernetes(service))
			}
		}
		slices.SortFunc(snapshot.Services, func(left, right serviceDocument) int {
			return strings.Compare(left.Name, right.Name)
		})
	}
	feed.hub.mu.Lock()
	feed.sequence++
	snapshot.Sequence = feed.sequence
	snapshot.ResourceVersion = feed.version
	for _, subscriber := range feed.subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			select {
			case subscriber <- snapshot:
			default:
			}
		}
	}
	feed.hub.mu.Unlock()
}
