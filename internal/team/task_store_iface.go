package team

// TaskStoreInterface abstracts task storage operations. All methods are scoped
// by (namespace, teamName) so the store is correct across namespaces.
// Implemented by the in-memory TaskStore (tests) and K8sTaskStore (production).
type TaskStoreInterface interface {
	Create(namespace, teamName, subject, description string, blockedBy []string) (*TeamTask, error)
	Get(namespace, teamName, taskID string) (*TeamTask, error)
	List(namespace, teamName string) []*TeamTask
	Claim(namespace, teamName, taskID, claimedBy string, expectedVersion int64) (*TeamTask, error)
	Complete(namespace, teamName, taskID, result string) (*TeamTask, error)
	Unclaim(namespace, teamName, taskID string) (*TeamTask, error)
	GetUnblockedPending(namespace, teamName string) []*TeamTask
	GetSummary(namespace, teamName string) (total, pending, inProgress, completed int32)
	DeleteTeam(namespace, teamName string)
}
