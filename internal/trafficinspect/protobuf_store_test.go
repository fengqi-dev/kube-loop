package trafficinspect

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
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
	if err := os.WriteFile(filepath.Join(root, "broken.proto"), []byte(`syntax = "proto3"; this is invalid`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.ReplaceDirectory(context.Background(), root); err == nil {
		t.Fatal("invalid protobuf replacement succeeded")
	}
	if got := store.Files(); !reflect.DeepEqual(got, []string{"valid.proto"}) {
		t.Fatalf("active schemas changed after failed import: %v", got)
	}
}
