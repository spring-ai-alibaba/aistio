package team

import (
	"fmt"
	"sync"
	"time"
)

// TaskState represents the status of a team task.
type TaskState string

const (
	TaskStatePending    TaskState = "pending"
	TaskStateInProgress TaskState = "in_progress"
	TaskStateCompleted  TaskState = "completed"
)

// TeamTask represents a task in the distributed task list.
type TeamTask struct {
	ID              string    `json:"id"`
	TeamName        string    `json:"teamName"`
	Subject         string    `json:"subject"`
	Description     string    `json:"description,omitempty"`
	State           TaskState `json:"state"`
	Owner           string    `json:"owner,omitempty"`
	BlockedBy       []string  `json:"blockedBy,omitempty"`
	Blocks          []string  `json:"blocks,omitempty"`
	ResourceVersion int64     `json:"resourceVersion"`
	CreatedAt       string    `json:"createdAt"`
	CompletedAt     string    `json:"completedAt,omitempty"`
	Result          string    `json:"result,omitempty"`
}

// TaskStore is an in-memory TaskStoreInterface used by unit tests. Production
// uses K8sTaskStore. State is keyed by (namespace/teamName).
type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]map[string]*TeamTask // nsTeamKey -> taskID -> task
	seq   map[string]int64                // nsTeamKey -> next task sequence
}

// NewTaskStore creates a new in-memory TaskStore.
func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: make(map[string]map[string]*TeamTask),
		seq:   make(map[string]int64),
	}
}

func nsTeamKey(namespace, teamName string) string {
	return namespace + "/" + teamName
}

// Create adds a new task to a team's task list.
func (s *TaskStore) Create(namespace, teamName, subject, description string, blockedBy []string) (*TeamTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := nsTeamKey(namespace, teamName)
	if s.tasks[key] == nil {
		s.tasks[key] = make(map[string]*TeamTask)
	}

	s.seq[key]++
	id := fmt.Sprintf("task-%d", s.seq[key])

	task := &TeamTask{
		ID:              id,
		TeamName:        teamName,
		Subject:         subject,
		Description:     description,
		State:           TaskStatePending,
		BlockedBy:       blockedBy,
		ResourceVersion: 1,
		CreatedAt:       time.Now().Format(time.RFC3339),
	}

	s.tasks[key][id] = task
	return task, nil
}

// Get retrieves a task by team and ID.
func (s *TaskStore) Get(namespace, teamName, taskID string) (*TeamTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	teamTasks, ok := s.tasks[nsTeamKey(namespace, teamName)]
	if !ok {
		return nil, fmt.Errorf("team %s not found", teamName)
	}
	task, ok := teamTasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found in team %s", taskID, teamName)
	}
	return task, nil
}

// List returns all tasks for a team.
func (s *TaskStore) List(namespace, teamName string) []*TeamTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	teamTasks := s.tasks[nsTeamKey(namespace, teamName)]
	result := make([]*TeamTask, 0, len(teamTasks))
	for _, t := range teamTasks {
		result = append(result, t)
	}
	return result
}

// Claim attempts to claim a task with optimistic concurrency.
// Returns error if the resourceVersion doesn't match (409 Conflict scenario).
func (s *TaskStore) Claim(namespace, teamName, taskID, claimedBy string, expectedVersion int64) (*TeamTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	teamTasks, ok := s.tasks[nsTeamKey(namespace, teamName)]
	if !ok {
		return nil, fmt.Errorf("team %s not found", teamName)
	}
	task, ok := teamTasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	if task.State != TaskStatePending {
		return nil, fmt.Errorf("task %s is not pending (current state: %s)", taskID, task.State)
	}

	if s.isBlocked(namespace, teamName, task) {
		return nil, fmt.Errorf("task %s is blocked by incomplete dependencies", taskID)
	}

	if expectedVersion > 0 && task.ResourceVersion != expectedVersion {
		return task, fmt.Errorf("conflict: expected version %d but current is %d", expectedVersion, task.ResourceVersion)
	}

	task.State = TaskStateInProgress
	task.Owner = claimedBy
	task.ResourceVersion++
	return task, nil
}

// Complete marks a task as completed and unblocks dependent tasks.
func (s *TaskStore) Complete(namespace, teamName, taskID, result string) (*TeamTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	teamTasks, ok := s.tasks[nsTeamKey(namespace, teamName)]
	if !ok {
		return nil, fmt.Errorf("team %s not found", teamName)
	}
	task, ok := teamTasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	if task.State != TaskStateInProgress {
		return nil, fmt.Errorf("task %s is not in_progress (current state: %s)", taskID, task.State)
	}

	task.State = TaskStateCompleted
	task.CompletedAt = time.Now().Format(time.RFC3339)
	task.Result = result
	task.ResourceVersion++

	return task, nil
}

// Unclaim releases a task back to pending state.
func (s *TaskStore) Unclaim(namespace, teamName, taskID string) (*TeamTask, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	teamTasks, ok := s.tasks[nsTeamKey(namespace, teamName)]
	if !ok {
		return nil, fmt.Errorf("team %s not found", teamName)
	}
	task, ok := teamTasks[taskID]
	if !ok {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	task.State = TaskStatePending
	task.Owner = ""
	task.ResourceVersion++
	return task, nil
}

// GetUnblockedPending returns tasks that are pending and not blocked.
func (s *TaskStore) GetUnblockedPending(namespace, teamName string) []*TeamTask {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*TeamTask
	teamTasks := s.tasks[nsTeamKey(namespace, teamName)]
	for _, task := range teamTasks {
		if task.State == TaskStatePending && !s.isBlocked(namespace, teamName, task) {
			result = append(result, task)
		}
	}
	return result
}

// GetSummary returns aggregate task counts for a team.
func (s *TaskStore) GetSummary(namespace, teamName string) (total, pending, inProgress, completed int32) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, task := range s.tasks[nsTeamKey(namespace, teamName)] {
		total++
		switch task.State {
		case TaskStatePending:
			pending++
		case TaskStateInProgress:
			inProgress++
		case TaskStateCompleted:
			completed++
		}
	}
	return
}

// DeleteTeam removes all tasks for a team.
func (s *TaskStore) DeleteTeam(namespace, teamName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := nsTeamKey(namespace, teamName)
	delete(s.tasks, key)
	delete(s.seq, key)
}

// isBlocked checks if a task has incomplete dependencies (must hold read lock).
func (s *TaskStore) isBlocked(namespace, teamName string, task *TeamTask) bool {
	teamTasks := s.tasks[nsTeamKey(namespace, teamName)]
	for _, depID := range task.BlockedBy {
		dep, ok := teamTasks[depID]
		if !ok {
			continue
		}
		if dep.State != TaskStateCompleted {
			return true
		}
	}
	return false
}
