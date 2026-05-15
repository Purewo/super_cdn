package deploymentevidence

import "testing"

func TestOperationSupport(t *testing.T) {
	if !IsCloudflareStaticOperation("") || !IsCloudflareStaticOperation(OperationDeploy) || !IsCloudflareStaticOperation(OperationRollbackApply) {
		t.Fatalf("cloudflare static supported operations were rejected")
	}
	if IsCloudflareStaticOperation(OperationWriteback) {
		t.Fatalf("cloudflare static must not accept writeback operation")
	}
	if !IsHybridEdgeOperation("") || !IsHybridEdgeOperation(OperationDeploy) || !IsHybridEdgeOperation(OperationRollbackApply) || !IsHybridEdgeOperation(OperationWriteback) {
		t.Fatalf("hybrid edge supported operations were rejected")
	}
	if IsHybridEdgeOperation("unknown") {
		t.Fatalf("unknown hybrid edge operation was accepted")
	}
	if NormalizeOperation("  "+OperationWriteback+"  ") != OperationWriteback {
		t.Fatalf("operation normalization failed")
	}
}
