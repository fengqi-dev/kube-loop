package kubeapi

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiMeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/fengqi-dev/kube-loop/internal/controlplane/authorization"
	"github.com/fengqi-dev/kube-loop/internal/controlplane/controlplaneapi"
	"github.com/fengqi-dev/kube-loop/internal/protocol/websocket"
)

const defaultInventoryResync = 30 * time.Second

type inventoryResource string

const (
	inventoryPods     inventoryResource = "pods"
	inventoryServices inventoryResource = "services"
)

type inventorySnapshot struct {
	SchemaVersion   int               `json:"schemaVersion"`
	Type            string            `json:"type"`
	Resource        inventoryResource `json:"resource"`
	Namespace       string            `json:"namespace"`
	ResourceVersion string            `json:"resourceVersion,omitempty"`
	Sequence        uint64            `json:"sequence"`
	GeneratedAt     time.Time         `json:"generatedAt"`
	Pods            []podDocument     `json:"pods,omitempty"`
	Services        []serviceDocument `json:"services,omitempty"`
}

type inventoryWatchKey struct {
	Subject   [sha256.Size]byte
	Namespace string
	Resource  inventoryResource
}

type inventoryWatchHub struct {
	mu     sync.Mutex
	resync time.Duration
	feeds  map[inventoryWatchKey]*inventoryFeed
	nextID atomic.Uint64
}

type inventoryFeed struct {
	hub         *inventoryWatchHub
	key         inventoryWatchKey
	factory     informers.SharedInformerFactory
	informer    cache.SharedIndexInformer
	stop        chan struct{}
	ready       chan struct{}
	dirty       chan struct{}
	readyErr    error
	sequence    uint64
	version     string
	subscribers map[uint64]chan inventorySnapshot
}

func newInventoryWatchHub(resync time.Duration) *inventoryWatchHub {
	return &inventoryWatchHub{
		resync: resync,
		feeds:  make(map[inventoryWatchKey]*inventoryFeed),
	}
}

func (hub *inventoryWatchHub) subscribe(
	ctx context.Context,
	subject authorization.Subject,
	client kubernetes.Interface,
	namespace string,
	resource inventoryResource,
) (<-chan inventorySnapshot, func(), error) {
	if hub == nil || client == nil {
		return nil, nil, errors.New("inventory Watch is unavailable")
	}
	key := inventoryWatchKey{
		Subject:   inventorySubjectKey(subject),
		Namespace: namespace,
		Resource:  resource,
	}
	id := hub.nextID.Add(1)
	updates := make(chan inventorySnapshot, 1)
	hub.mu.Lock()
	feed := hub.feeds[key]
	if feed == nil {
		feed = hub.newFeed(key, client)
		hub.feeds[key] = feed
		go feed.run()
	}
	feed.subscribers[id] = updates
	hub.mu.Unlock()
	unsubscribe := func() { hub.unsubscribe(key, id) }
	select {
	case <-ctx.Done():
		unsubscribe()
		return nil, nil, ctx.Err()
	case <-feed.ready:
		if feed.readyErr != nil {
			unsubscribe()
			return nil, nil, feed.readyErr
		}
	}
	feed.schedule()
	return updates, unsubscribe, nil
}

func (hub *inventoryWatchHub) newFeed(
	key inventoryWatchKey,
	client kubernetes.Interface,
) *inventoryFeed {
	factory := informers.NewSharedInformerFactoryWithOptions(
		client,
		hub.resync,
		informers.WithNamespace(key.Namespace),
	)
	var informer cache.SharedIndexInformer
	if key.Resource == inventoryPods {
		informer = factory.Core().V1().Pods().Informer()
	} else {
		informer = factory.Core().V1().Services().Informer()
	}
	feed := &inventoryFeed{
		hub: hub, key: key, factory: factory, informer: informer, stop: make(chan struct{}), ready: make(chan struct{}),
		dirty: make(
			chan struct{},
			1,
		), subscribers: make(map[uint64]chan inventorySnapshot),
	}
	_, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(object any) { feed.changed(object) },
		UpdateFunc: func(_, object any) { feed.changed(object) },
		DeleteFunc: func(object any) { feed.changed(object) },
	})
	if err != nil {
		feed.readyErr = err
	}
	return feed
}

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

func (hub *inventoryWatchHub) unsubscribe(key inventoryWatchKey, id uint64) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	feed := hub.feeds[key]
	if feed == nil {
		return
	}
	delete(feed.subscribers, id)
	if len(feed.subscribers) == 0 {
		delete(hub.feeds, key)
		close(feed.stop)
	}
}

func inventorySubjectKey(subject authorization.Subject) [sha256.Size]byte {
	groups := append([]string(nil), subject.Groups...)
	slices.Sort(groups)
	return sha256.Sum256(
		[]byte(subject.ID + "\x00" + strings.Join(groups, "\x00")),
	)
}

func (handler *Service) watchInventory(
	writer http.ResponseWriter,
	request *http.Request,
	client kubernetes.Interface,
	identity controlplaneapi.Identity,
	namespace string,
	resource inventoryResource,
) *controlplaneapi.Error {
	if len(request.URL.Query()) != 1 ||
		len(request.URL.Query()[operationWatch]) != 1 ||
		request.URL.Query().Get(operationWatch) != "true" {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeInvalidArgument,
			Field:   operationWatch,
			Message: "watch=true is required",
		}
	}
	watchContext := request.Context()
	cancel := func() {}
	if !identity.AccessExpiresAt.IsZero() {
		watchContext, cancel = context.WithDeadline(
			watchContext,
			identity.AccessExpiresAt,
		)
	}
	defer cancel()
	updates, unsubscribe, err := handler.inventory.subscribe(
		watchContext,
		authorization.Subject{
			ID: identity.Subject, Groups: append([]string(nil), identity.Groups...),
		},
		client,
		namespace,
		resource,
	)
	if err != nil {
		return &controlplaneapi.Error{
			Code:    controlplaneapi.CodeUnavailable,
			Message: "Inventory Watch is unavailable",
			Cause:   err,
		}
	}
	defer unsubscribe()
	connection, err := websocket.Accept(
		writer,
		request,
		&websocket.AcceptOptions{
			CompressionMode: websocket.CompressionDisabled,
		},
	)
	if err != nil {
		return nil
	}
	defer func() { _ = connection.CloseNow() }()
	for {
		select {
		case <-watchContext.Done():
			_ = connection.Close(
				websocket.StatusNormalClosure,
				"Inventory Watch closed",
			)
			return nil
		case snapshot := <-updates:
			encoded, encodeErr := json.Marshal(snapshot)
			if encodeErr != nil {
				_ = connection.Close(
					websocket.StatusInternalError,
					"Inventory Watch encoding failed",
				)
				return nil
			}
			if writeErr := connection.Write(watchContext, websocket.MessageText, encoded); writeErr != nil {
				return nil
			}
		}
	}
}
