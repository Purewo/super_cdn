package storage

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLimitedStoreEnforcesDirectStorageConstraints(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	source := filepath.Join(root, "source.txt")
	if err := os.WriteFile(source, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	local, err := NewLocalStore("ipfs_pinata", filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	maxBatch := 2
	maxFileSize := int64(10)
	dailyLimit := int64(6)
	limited := NewLimitedStore(local, ResourceLibraryPolicy{}, BindingConstraints{
		MaxBatchFiles:         &maxBatch,
		MaxFileSizeBytes:      &maxFileSize,
		DailyUploadLimitBytes: &dailyLimit,
	})
	if _, err := limited.PreflightPut(ctx, PreflightOptions{TotalSize: 5, LargestFileSize: 5, BatchFileCount: 3}); err == nil {
		t.Fatal("expected preflight batch rejection")
	}
	if _, err := limited.PreflightPut(ctx, PreflightOptions{TotalSize: 11, LargestFileSize: 11, BatchFileCount: 1}); err == nil {
		t.Fatal("expected preflight file-size rejection")
	}
	if _, err := limited.Put(ctx, PutOptions{Key: "a.txt", FilePath: source, Size: 5, BatchFileCount: 1}); err != nil {
		t.Fatalf("first upload failed: %v", err)
	}
	if _, err := limited.Put(ctx, PutOptions{Key: "b.txt", FilePath: source, Size: 5, BatchFileCount: 1}); err == nil {
		t.Fatal("expected daily limit rejection")
	}
	if limited.Type() != "local" {
		t.Fatalf("limited store should preserve underlying type, got %q", limited.Type())
	}
}

func TestLimitedStorePolicyPreflight(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	local, err := NewLocalStore("overseas_accel", filepath.Join(root, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	available := int64(10)
	reserve := int64(3)
	capacity := int64(100)
	limited := NewLimitedStore(local, ResourceLibraryPolicy{
		TotalCapacityBytes: &capacity,
		AvailableBytes:     &available,
		ReserveBytes:       &reserve,
	}, BindingConstraints{})
	result, err := limited.PreflightPut(ctx, PreflightOptions{TotalSize: 7, LargestFileSize: 7, BatchFileCount: 1})
	if err != nil {
		t.Fatalf("preflight should fit effective available capacity: %v", err)
	}
	if result.LibrarySummary == nil || result.LibrarySummary.EffectiveAvailableBytes == nil || *result.LibrarySummary.EffectiveAvailableBytes != 7 {
		t.Fatalf("unexpected policy summary: %+v", result.LibrarySummary)
	}
	if _, err := limited.PreflightPut(ctx, PreflightOptions{TotalSize: 8, LargestFileSize: 8, BatchFileCount: 1}); err == nil {
		t.Fatal("expected effective available capacity rejection")
	}
}
