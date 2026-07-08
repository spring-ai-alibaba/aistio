package team

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

const teamLabel = "agentscope.io/team"

// K8sTaskStore implements TaskStoreInterface using TeamTask CRDs. It is the
// production store: state is persisted and consistent across replicas.
type K8sTaskStore struct {
	client client.Client
	ctx    context.Context
}

// NewK8sTaskStore creates a new K8sTaskStore.
func NewK8sTaskStore(c client.Client) *K8sTaskStore {
	return &K8sTaskStore{client: c, ctx: context.Background()}
}

// WithContext returns a copy with the given context.
func (s *K8sTaskStore) WithContext(ctx context.Context) *K8sTaskStore {
	return &K8sTaskStore{client: s.client, ctx: ctx}
}

// ownerRefFor fetches the parent AgentTeam and returns a controller owner
// reference so child objects are garbage-collected with the team. Best-effort:
// returns nil if the team can't be resolved.
func ownerRefFor(ctx context.Context, c client.Client, namespace, teamName string) []metav1.OwnerReference {
	var at v1alpha1.AgentTeam
	if err := c.Get(ctx, client.ObjectKey{Name: teamName, Namespace: namespace}, &at); err != nil {
		return nil
	}
	ref := metav1.NewControllerRef(&at, v1alpha1.GroupVersion.WithKind("AgentTeam"))
	return []metav1.OwnerReference{*ref}
}

func (s *K8sTaskStore) Create(namespace, teamName, subject, description string, blockedBy []string) (*TeamTask, error) {
	task := &v1alpha1.TeamTask{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName:    fmt.Sprintf("%s-task-", teamName),
			Namespace:       namespace,
			Labels:          map[string]string{teamLabel: teamName},
			OwnerReferences: ownerRefFor(s.ctx, s.client, namespace, teamName),
		},
		Spec: v1alpha1.TeamTaskSpec{
			TeamRef:     teamName,
			Subject:     subject,
			Description: description,
			BlockedBy:   blockedBy,
		},
	}
	if err := s.client.Create(s.ctx, task); err != nil {
		return nil, err
	}
	task.Status.State = v1alpha1.TeamTaskStatePending
	if err := s.client.Status().Update(s.ctx, task); err != nil {
		// non-fatal, task was created
		_ = err
	}
	return s.toTeamTask(task), nil
}

func (s *K8sTaskStore) Get(namespace, teamName, taskID string) (*TeamTask, error) {
	var task v1alpha1.TeamTask
	if err := s.client.Get(s.ctx, client.ObjectKey{Name: taskID, Namespace: namespace}, &task); err != nil {
		return nil, err
	}
	return s.toTeamTask(&task), nil
}

func (s *K8sTaskStore) List(namespace, teamName string) []*TeamTask {
	var list v1alpha1.TeamTaskList
	if err := s.client.List(s.ctx, &list,
		client.InNamespace(namespace),
		client.MatchingLabels{teamLabel: teamName},
	); err != nil {
		return nil
	}
	result := make([]*TeamTask, 0, len(list.Items))
	for i := range list.Items {
		result = append(result, s.toTeamTask(&list.Items[i]))
	}
	return result
}

func (s *K8sTaskStore) Claim(namespace, teamName, taskID, claimedBy string, expectedVersion int64) (*TeamTask, error) {
	var task v1alpha1.TeamTask
	if err := s.client.Get(s.ctx, client.ObjectKey{Name: taskID, Namespace: namespace}, &task); err != nil {
		return nil, err
	}
	if task.Status.State != v1alpha1.TeamTaskStatePending {
		return s.toTeamTask(&task), fmt.Errorf("task %s is not pending", taskID)
	}
	task.Status.State = v1alpha1.TeamTaskStateInProgress
	task.Status.Owner = claimedBy
	if err := s.client.Status().Update(s.ctx, &task); err != nil {
		if errors.IsConflict(err) {
			return s.toTeamTask(&task), fmt.Errorf("conflict: task was modified by another writer")
		}
		return nil, err
	}
	return s.toTeamTask(&task), nil
}

func (s *K8sTaskStore) Complete(namespace, teamName, taskID, result string) (*TeamTask, error) {
	var task v1alpha1.TeamTask
	if err := s.client.Get(s.ctx, client.ObjectKey{Name: taskID, Namespace: namespace}, &task); err != nil {
		return nil, err
	}
	task.Status.State = v1alpha1.TeamTaskStateCompleted
	task.Status.CompletedAt = time.Now().Format(time.RFC3339)
	task.Status.Result = result
	if err := s.client.Status().Update(s.ctx, &task); err != nil {
		return nil, err
	}
	return s.toTeamTask(&task), nil
}

func (s *K8sTaskStore) Unclaim(namespace, teamName, taskID string) (*TeamTask, error) {
	var task v1alpha1.TeamTask
	if err := s.client.Get(s.ctx, client.ObjectKey{Name: taskID, Namespace: namespace}, &task); err != nil {
		return nil, err
	}
	task.Status.State = v1alpha1.TeamTaskStatePending
	task.Status.Owner = ""
	if err := s.client.Status().Update(s.ctx, &task); err != nil {
		return nil, err
	}
	return s.toTeamTask(&task), nil
}

func (s *K8sTaskStore) GetUnblockedPending(namespace, teamName string) []*TeamTask {
	all := s.List(namespace, teamName)
	completedIDs := make(map[string]bool)
	for _, t := range all {
		if t.State == TaskStateCompleted {
			completedIDs[t.ID] = true
		}
	}
	var result []*TeamTask
	for _, t := range all {
		if t.State != TaskStatePending {
			continue
		}
		blocked := false
		for _, dep := range t.BlockedBy {
			if !completedIDs[dep] {
				blocked = true
				break
			}
		}
		if !blocked {
			result = append(result, t)
		}
	}
	return result
}

func (s *K8sTaskStore) GetSummary(namespace, teamName string) (total, pending, inProgress, completed int32) {
	for _, t := range s.List(namespace, teamName) {
		total++
		switch t.State {
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

func (s *K8sTaskStore) DeleteTeam(namespace, teamName string) {
	_ = s.client.DeleteAllOf(s.ctx, &v1alpha1.TeamTask{},
		client.InNamespace(namespace),
		client.MatchingLabels{teamLabel: teamName},
	)
}

func (s *K8sTaskStore) toTeamTask(crd *v1alpha1.TeamTask) *TeamTask {
	return &TeamTask{
		ID:              crd.Name,
		TeamName:        crd.Spec.TeamRef,
		Subject:         crd.Spec.Subject,
		Description:     crd.Spec.Description,
		State:           TaskState(crd.Status.State),
		Owner:           crd.Status.Owner,
		BlockedBy:       crd.Spec.BlockedBy,
		ResourceVersion: 0, // K8s handles OCC via object resourceVersion
		CreatedAt:       crd.CreationTimestamp.Format(time.RFC3339),
		CompletedAt:     crd.Status.CompletedAt,
		Result:          crd.Status.Result,
	}
}
