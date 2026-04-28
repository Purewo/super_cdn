package storage

import "testing"

func TestR2MultipartPlan(t *testing.T) {
	if r2UseMultipart(r2MultipartThreshold) {
		t.Fatal("threshold-sized uploads should still use single PutObject")
	}
	if !r2UseMultipart(r2MultipartThreshold + 1) {
		t.Fatal("uploads above threshold should use multipart")
	}
	partSize, parts := r2MultipartPlan(r2MultipartThreshold + 1)
	if partSize != r2MultipartPartSize || parts != 3 {
		t.Fatalf("unexpected default multipart plan partSize=%d parts=%d", partSize, parts)
	}
	huge := r2MultipartPartSize*r2MultipartMaxParts + 1
	partSize, parts = r2MultipartPlan(huge)
	if int64(parts) > r2MultipartMaxParts {
		t.Fatalf("too many parts: partSize=%d parts=%d", partSize, parts)
	}
	if partSize <= r2MultipartPartSize {
		t.Fatalf("expected larger part size for huge upload, got %d", partSize)
	}
}
