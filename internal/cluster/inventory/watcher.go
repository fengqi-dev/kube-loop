package inventory

import (
	"context"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

// Snapshot contains the Kubernetes resources currently held by the informer
// caches. Domain-facing packages decide how those resources are represented.
type Snapshot struct {
	Pods        []*corev1.Pod
	Services    []*corev1.Service
	Deployments []*appsv1.Deployment
}

type watcher struct {
	cancel    context.CancelFunc
	factories []informers.SharedInformerFactory
	onChange  func(Snapshot)
	debounce  *time.Timer
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

// Watch starts shared informers for Pods, Services, and Deployments.
// An empty namespace list watches all namespaces.
func Watch(
	ctx context.Context,
	client kubernetes.Interface,
	namespaces []string,
	onChange func(Snapshot),
) (io.Closer, error) {
	if client == nil {
		return nil, fmt.Errorf("kubernetes client is required")
	}
	if onChange == nil {
		return nil, fmt.Errorf("inventory callback is required")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	current := &watcher{
		cancel:   cancel,
		onChange: onChange,
	}

	targets := namespaces
	if len(targets) == 0 {
		targets = []string{""}
	}
	synced := make([]cache.InformerSynced, 0, len(targets)*3)
	for _, namespace := range targets {
		factory, syncers, err := startNamespaceInformers(client, namespace, current)
		if err != nil {
			cancel()
			return nil, err
		}
		current.factories = append(current.factories, factory)
		synced = append(synced, syncers...)
		factory.Start(watchCtx.Done())
	}

	if !cache.WaitForCacheSync(watchCtx.Done(), synced...) {
		cancel()
		return nil, fmt.Errorf("timed out waiting for inventory informers")
	}
	current.emit()
	return current, nil
}

func startNamespaceInformers(
	client kubernetes.Interface,
	namespace string,
	current *watcher,
) (informers.SharedInformerFactory, []cache.InformerSynced, error) {
	var factory informers.SharedInformerFactory
	if namespace == "" {
		factory = informers.NewSharedInformerFactory(client, 0)
	} else {
		factory = informers.NewSharedInformerFactoryWithOptions(
			client, 0, informers.WithNamespace(namespace),
		)
	}
	podInformer := factory.Core().V1().Pods().Informer()
	serviceInformer := factory.Core().V1().Services().Informer()
	deploymentInformer := factory.Apps().V1().Deployments().Informer()
	handler := cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { current.schedule() },
		UpdateFunc: func(any, any) { current.schedule() },
		DeleteFunc: func(any) { current.schedule() },
	}
	if _, err := podInformer.AddEventHandler(handler); err != nil {
		return nil, nil, fmt.Errorf("watch pods: %w", err)
	}
	if _, err := serviceInformer.AddEventHandler(handler); err != nil {
		return nil, nil, fmt.Errorf("watch services: %w", err)
	}
	if _, err := deploymentInformer.AddEventHandler(handler); err != nil {
		return nil, nil, fmt.Errorf("watch deployments: %w", err)
	}
	return factory, []cache.InformerSynced{
		podInformer.HasSynced, serviceInformer.HasSynced, deploymentInformer.HasSynced,
	}, nil
}

func (w *watcher) Close() error {
	w.closeOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		if w.debounce != nil {
			w.debounce.Stop()
		}
		w.mu.Unlock()
		w.cancel()
		for _, factory := range w.factories {
			factory.Shutdown()
		}
	})
	return nil
}

func (w *watcher) schedule() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	if w.debounce != nil {
		w.debounce.Stop()
	}
	w.debounce = time.AfterFunc(300*time.Millisecond, w.emit)
}

func (w *watcher) emit() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	onChange := w.onChange
	factories := append([]informers.SharedInformerFactory{}, w.factories...)
	w.mu.Unlock()

	var snapshot Snapshot
	for _, factory := range factories {
		pods, err := factory.Core().V1().Pods().Lister().List(labels.Everything())
		if err != nil {
			log.Printf("inventory watcher list pods: %v", err)
			return
		}
		snapshot.Pods = append(snapshot.Pods, pods...)
		services, err := factory.Core().V1().Services().Lister().List(labels.Everything())
		if err != nil {
			log.Printf("inventory watcher list services: %v", err)
			return
		}
		snapshot.Services = append(snapshot.Services, services...)
		deployments, err := factory.Apps().V1().Deployments().Lister().List(labels.Everything())
		if err != nil {
			log.Printf("inventory watcher list deployments: %v", err)
			return
		}
		snapshot.Deployments = append(snapshot.Deployments, deployments...)
	}
	onChange(snapshot)
}
