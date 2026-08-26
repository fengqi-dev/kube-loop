package trafficinspect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fengqi-dev/kube-loop/internal/utils"
)

func TestProtobufSchemaStoreImportsNestedDirectoryAndReloads(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "example"), 0o700); err != nil {
		t.Fatal(err)
	}
	common := `syntax = "proto3"; package example.v1; message EchoMessage { string text = 1; }`
	service := `syntax = "proto3"; package example.v1; import "example/common.proto";
service EchoService { rpc Echo(EchoMessage) returns (EchoMessage); }`
	if err := os.WriteFile(filepath.Join(root, "example", "common.proto"), []byte(common), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "service.proto"), []byte(service), 0o600); err != nil {
		t.Fatal(err)
	}

	storePath := filepath.Join(t.TempDir(), "schemas.json")
	store, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err != nil {
		t.Fatalf("import proto directory: %v", err)
	}
	want := []string{"example/common.proto", "service.proto"}
	if got := store.Files(); !reflect.DeepEqual(got, want) {
		t.Fatalf("schema files = %v, want %v", got, want)
	}

	reloaded, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatalf("reload proto schemas: %v", err)
	}
	if got := reloaded.Files(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded schema files = %v, want %v", got, want)
	}
}

func TestProtobufSchemaStoreRejectsInvalidReplacementWithoutChangingActiveSet(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "valid.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewProtobufSchemaStore(filepath.Join(t.TempDir(), "schemas.json"), NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "broken.proto"),
		[]byte(`syntax = "proto3"; this is invalid`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err == nil {
		t.Fatal("invalid protobuf replacement succeeded")
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"valid.proto"}) {
		t.Fatalf("active schemas changed after failed import: %v", got)
	}
}

func TestProtobufSchemaStoreActivatesPersistedCompileAfterContextCancellation(t *testing.T) {
	initial := t.TempDir()
	if err := os.WriteFile(filepath.Join(initial, "initial.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	storePath := filepath.Join(t.TempDir(), "schemas.json")
	store, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), initial); err != nil {
		t.Fatal(err)
	}
	replacement := t.TempDir()
	if err := os.WriteFile(filepath.Join(replacement, "replacement.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		err := utils.WriteFile(path, raw, dirMode, fileMode)
		cancel()
		return err
	}
	if err := store.ReplaceDirectory(ctx, replacement); err != nil {
		t.Fatalf("replace after durable write: %v", err)
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"replacement.proto"}) {
		t.Fatalf("active schemas after durable write = %v", got)
	}
	reloaded, err := NewProtobufSchemaStore(storePath, NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	if err := reloaded.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := reloaded.Files(); !reflect.DeepEqual(got, []string{"replacement.proto"}) {
		t.Fatalf("persisted schemas after durable write = %v", got)
	}
}

func TestProtobufSchemaStoreSerializesConcurrentReplacements(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	if err := os.WriteFile(filepath.Join(first, "first.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "second.proto"), []byte(testProto), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewProtobufSchemaStore(filepath.Join(t.TempDir(), "schemas.json"), NewProtobufDecoder())
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	store.writeFile = func(path string, raw []byte, dirMode, fileMode os.FileMode) error {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return utils.WriteFile(path, raw, dirMode, fileMode)
	}
	firstDone := make(chan error, 1)
	go func() { firstDone <- store.ReplaceDirectory(context.Background(), first) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first protobuf replacement did not start persistence")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- store.ReplaceDirectory(context.Background(), second) }()
	select {
	case err := <-secondDone:
		t.Fatalf("second replacement bypassed the first transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	releaseOnce.Do(func() { close(release) })
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"second.proto"}) {
		t.Fatalf("final schemas = %v", got)
	}
}
