package mcp

import (
	"sync"
	"testing"
)

type memoryConfigStore struct {
	mu     sync.Mutex
	config Config
}

func (store *memoryConfigStore) Load() (Config, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.config, nil
}

func (store *memoryConfigStore) Save(config Config) error {
	store.mu.Lock()
	store.config = config
	store.mu.Unlock()
	return nil
}

func TestControllerPortChangeRestartsListener(t *testing.T) {
	firstPort, secondPort := freePort(t), freePort(t)
	for secondPort == firstPort {
		secondPort = freePort(t)
	}
	store := &memoryConfigStore{config: Config{Port: firstPort, TokenEnabled: false}}
	controller, err := NewController(&fakeBackend{}, store, "test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = controller.Stop() }()
	if err := controller.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	if controller.Status().Port != firstPort || !controller.Status().Listening {
		t.Fatalf("first status=%#v", controller.Status())
	}
	if err := controller.SetPort(secondPort); err != nil {
		t.Fatal(err)
	}
	status := controller.Status()
	if status.Port != secondPort || !status.Listening {
		t.Fatalf("second status=%#v", status)
	}
	if store.config.Port != secondPort {
		t.Fatalf("persisted config=%#v", store.config)
	}
}
