package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResourceLibraryWritesOnlyFirstBinding(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := NewLocalStore("first", filepath.Join(root, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLocalStore("second", filepath.Join(root, "second"))
	if err != nil {
		t.Fatal(err)
	}
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{
		{Name: "first_path", Store: first},
		{Name: "second_path", Store: second},
	})
	if err != nil {
		t.Fatal(err)
	}
	locator, err := library.Put(ctx, PutOptions{
		Key:      "objects/a.txt",
		FilePath: source,
		Size:     5,
		SHA256:   "hash",
	})
	if err != nil {
		t.Fatal(err)
	}
	bindingName, _, ok := decodeResourceLocator(locator)
	if !ok || bindingName != "first_path" {
		t.Fatalf("locator = %q binding=%q ok=%v", locator, bindingName, ok)
	}
	if _, err := first.Stat(ctx, "objects/a.txt"); err != nil {
		t.Fatalf("first binding missing object: %v", err)
	}
	if _, err := second.Stat(ctx, "objects/a.txt"); err == nil {
		t.Fatal("second binding should not receive batch write")
	}
}

func TestResourceLibraryPutWaitsForVerifiedLocator(t *testing.T) {
	oldAttempts := resourceLibraryPutStatAttempts
	oldDelay := resourceLibraryPutStatDelay
	resourceLibraryPutStatAttempts = 3
	resourceLibraryPutStatDelay = time.Millisecond
	t.Cleanup(func() {
		resourceLibraryPutStatAttempts = oldAttempts
		resourceLibraryPutStatDelay = oldDelay
	})

	store := &verifyAfterPutStore{name: "binding", statFailures: 1}
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{{
		Name:  "binding_path",
		Store: store,
	}})
	if err != nil {
		t.Fatal(err)
	}
	locator, err := library.Put(context.Background(), PutOptions{Key: "objects/a.txt", Size: 5})
	if err != nil {
		t.Fatal(err)
	}
	bindingName, innerLocator, ok := decodeResourceLocator(locator)
	if !ok || bindingName != "binding_path" || innerLocator != "https://signed.example/objects/a.txt" {
		t.Fatalf("locator = %q binding=%q inner=%q ok=%v", locator, bindingName, innerLocator, ok)
	}
	if store.statCalls != 2 {
		t.Fatalf("stat calls = %d", store.statCalls)
	}
}

func TestResourceLibraryGetFallsBackWhenLocatorBindingFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	first, err := NewLocalStore("first", filepath.Join(root, "first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewLocalStore("second", filepath.Join(root, "second"))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("from-backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Put(ctx, PutOptions{Key: "objects/a.txt", FilePath: source, Size: 11}); err != nil {
		t.Fatal(err)
	}
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{
		{Name: "first_path", Store: first},
		{Name: "second_path", Store: second},
	})
	if err != nil {
		t.Fatal(err)
	}

	stream, err := library.Get(ctx, "objects/a.txt", GetOptions{
		Locator: encodeResourceLocator("first_path", first.PublicURL("objects/a.txt")),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Body.Close()
	raw, err := io.ReadAll(stream.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "from-backup" {
		t.Fatalf("body = %q", raw)
	}
}

func TestResourceLibraryUploadConstraints(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStore("local", filepath.Join(root, "local"))
	if err != nil {
		t.Fatal(err)
	}
	maxBatch := 2
	maxFileSize := int64(10)
	dailyLimit := int64(6)
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{{
		Name:  "limited_path",
		Store: local,
		Constraints: BindingConstraints{
			MaxBatchFiles:         &maxBatch,
			MaxFileSizeBytes:      &maxFileSize,
			DailyUploadLimitBytes: &dailyLimit,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Put(ctx, PutOptions{Key: "a.txt", FilePath: source, Size: 5, BatchFileCount: 3}); err == nil {
		t.Fatal("expected batch file count rejection")
	}
	if _, err := library.Put(ctx, PutOptions{Key: "a.txt", FilePath: source, Size: 11, BatchFileCount: 1}); err == nil {
		t.Fatal("expected file size rejection")
	}
	if _, err := library.Put(ctx, PutOptions{Key: "a.txt", FilePath: source, Size: 5, BatchFileCount: 1}); err != nil {
		t.Fatalf("first upload failed: %v", err)
	}
	if _, err := library.Put(ctx, PutOptions{Key: "b.txt", FilePath: source, Size: 5, BatchFileCount: 1}); err == nil {
		t.Fatal("expected daily limit rejection")
	}
}

func TestResourceLibraryUnlimitedDailyUpload(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStore("local", filepath.Join(root, "local"))
	if err != nil {
		t.Fatal(err)
	}
	dailyLimit := int64(1)
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{{
		Name:  "unlimited_path",
		Store: local,
		Constraints: BindingConstraints{
			DailyUploadLimitBytes:     &dailyLimit,
			DailyUploadLimitUnlimited: true,
		},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := library.Put(ctx, PutOptions{Key: "a.txt", FilePath: source, Size: 5}); err != nil {
		t.Fatalf("unlimited daily upload should bypass byte limit: %v", err)
	}
}

func TestResourceLibraryPolicyPreflight(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	local, err := NewLocalStore("local", filepath.Join(root, "local"))
	if err != nil {
		t.Fatal(err)
	}
	available := int64(10)
	reserve := int64(3)
	capacity := int64(100)
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{{
		Name:  "path_a",
		Store: local,
	}}, ResourceLibraryPolicy{
		TotalCapacityBytes: &capacity,
		AvailableBytes:     &available,
		ReserveBytes:       &reserve,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := library.PreflightPut(ctx, PreflightOptions{TotalSize: 7, LargestFileSize: 7, BatchFileCount: 1})
	if err != nil {
		t.Fatalf("preflight should fit effective available capacity: %v", err)
	}
	if result.LibrarySummary == nil || result.LibrarySummary.EffectiveAvailableBytes == nil || *result.LibrarySummary.EffectiveAvailableBytes != 7 {
		t.Fatalf("unexpected library summary: %+v", result.LibrarySummary)
	}
	if _, err := library.PreflightPut(ctx, PreflightOptions{TotalSize: 8, LargestFileSize: 8, BatchFileCount: 1}); err == nil {
		t.Fatal("expected effective available capacity rejection")
	}
}

func TestResourceLibraryMaxBindingsPolicy(t *testing.T) {
	root := t.TempDir()
	local, err := NewLocalStore("local", filepath.Join(root, "local"))
	if err != nil {
		t.Fatal(err)
	}
	maxBindings := int64(1)
	_, err = NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{
		{Name: "path_a", Store: local},
		{Name: "path_b", Store: local},
	}, ResourceLibraryPolicy{MaxBindings: &maxBindings})
	if err == nil {
		t.Fatal("expected max bindings policy rejection")
	}
}

func TestResourceLibraryInitDirsWritesBindingStructure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	localRoot := filepath.Join(root, "local")
	local, err := NewLocalStore("local", localRoot)
	if err != nil {
		t.Fatal(err)
	}
	library, err := NewResourceLibraryStore("repo", []ResourceLibraryBindingStore{{
		Name:  "path_a",
		Path:  "/repo/path-a",
		Store: local,
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := library.InitDirs(ctx, InitOptions{
		Directories:   []string{"assets/objects", "sites/releases"},
		MarkerPath:    "_supercdn/init.json",
		MarkerPayload: []byte(`{"ok":true}`),
	})
	if err != nil {
		t.Fatalf("init dirs failed: %v", err)
	}
	if len(result.Bindings) != 1 {
		t.Fatalf("bindings = %d", len(result.Bindings))
	}
	for _, rel := range []string{"assets", "assets/objects", "sites", "sites/releases"} {
		if st, err := os.Stat(filepath.Join(localRoot, filepath.FromSlash(rel))); err != nil || !st.IsDir() {
			t.Fatalf("missing dir %s: stat=%v err=%v", rel, st, err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(localRoot, "_supercdn", "init.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("marker = %s", raw)
	}
}

type verifyAfterPutStore struct {
	name         string
	statFailures int
	statCalls    int
}

func (s *verifyAfterPutStore) Name() string { return s.name }
func (s *verifyAfterPutStore) Type() string { return "verify-test" }

func (s *verifyAfterPutStore) Put(context.Context, PutOptions) (string, error) {
	return "https://unsigned.example/object", nil
}

func (s *verifyAfterPutStore) Get(context.Context, string, GetOptions) (*ObjectStream, error) {
	return nil, ErrNotFound
}

func (s *verifyAfterPutStore) Stat(_ context.Context, key string) (*Stat, error) {
	s.statCalls++
	if s.statCalls <= s.statFailures {
		return nil, ErrNotFound
	}
	return &Stat{Locator: "https://signed.example/" + key}, nil
}

func (s *verifyAfterPutStore) Delete(context.Context, string) error { return nil }

func (s *verifyAfterPutStore) PublicURL(string) string { return "" }
