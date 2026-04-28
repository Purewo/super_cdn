package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalStoreRange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := NewLocalStore("local", filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Put(ctx, PutOptions{
		Key:         "a/source.txt",
		FilePath:    source,
		ContentType: "text/plain",
		SHA256:      "hash",
		Size:        10,
	}); err != nil {
		t.Fatal(err)
	}
	stream, err := store.Get(ctx, "a/source.txt", GetOptions{Range: "bytes=2-5"})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	body, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatal(err)
	}
	if stream.StatusCode != 206 {
		t.Fatalf("status = %d, want 206", stream.StatusCode)
	}
	if string(body) != "2345" {
		t.Fatalf("body = %q", string(body))
	}
	if stream.ContentRange != "bytes 2-5/10" {
		t.Fatalf("content range = %q", stream.ContentRange)
	}
}
