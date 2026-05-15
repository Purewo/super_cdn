package deploymentevidence

import "strings"

const (
	OperationDeploy        = "deploy"
	OperationWriteback     = "writeback"
	OperationRollbackApply = "rollback_apply"
)

func NormalizeOperation(value string) string {
	return strings.TrimSpace(value)
}

func IsCloudflareStaticOperation(value string) bool {
	switch NormalizeOperation(value) {
	case "", OperationDeploy, OperationRollbackApply:
		return true
	default:
		return false
	}
}

func IsHybridEdgeOperation(value string) bool {
	switch NormalizeOperation(value) {
	case "", OperationDeploy, OperationWriteback, OperationRollbackApply:
		return true
	default:
		return false
	}
}
