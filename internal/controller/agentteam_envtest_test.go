package controller_test

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	v1alpha1 "github.com/spring-ai-alibaba/aistio/api/v1alpha1"
)

const testTimeout = 10 * time.Second

// waitFor polls condition every 100ms until it returns true or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestAgentTeamCreation(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "team-create")
	ctx, cancel := testContext()
	defer cancel()

	team := &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-team",
			Namespace: ns,
		},
		Spec: v1alpha1.AgentTeamSpec{
			Objective: "Summarize daily reports",
			Lead: v1alpha1.TeamLeadSpec{
				AgentRef: v1alpha1.ObjectReference{Name: "lead-agent"},
			},
			Members: []v1alpha1.TeamMemberSpec{
				{
					Name:     "researcher",
					AgentRef: v1alpha1.ObjectReference{Name: "research-agent"},
				},
				{
					Name:     "writer",
					AgentRef: v1alpha1.ObjectReference{Name: "writer-agent"},
				},
			},
		},
	}

	// Create the AgentTeam
	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("failed to create AgentTeam: %v", err)
	}

	// Fetch it back and verify spec
	var fetched v1alpha1.AgentTeam
	key := types.NamespacedName{Name: "test-team", Namespace: ns}
	if err := k8sClient.Get(ctx, key, &fetched); err != nil {
		t.Fatalf("failed to get AgentTeam: %v", err)
	}

	if fetched.Spec.Objective != "Summarize daily reports" {
		t.Errorf("expected objective 'Summarize daily reports', got %q", fetched.Spec.Objective)
	}
	if fetched.Spec.Lead.AgentRef.Name != "lead-agent" {
		t.Errorf("expected lead agentRef 'lead-agent', got %q", fetched.Spec.Lead.AgentRef.Name)
	}
	if len(fetched.Spec.Members) != 2 {
		t.Fatalf("expected 2 members, got %d", len(fetched.Spec.Members))
	}
	if fetched.Spec.Members[0].Name != "researcher" {
		t.Errorf("expected first member 'researcher', got %q", fetched.Spec.Members[0].Name)
	}
	if fetched.Spec.Members[1].Name != "writer" {
		t.Errorf("expected second member 'writer', got %q", fetched.Spec.Members[1].Name)
	}
}

func TestAgentTeamReconcileSetsStatus(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "team-status")

	team := &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "status-team",
			Namespace: ns,
		},
		Spec: v1alpha1.AgentTeamSpec{
			Objective: "Test status transitions",
			Lead: v1alpha1.TeamLeadSpec{
				AgentRef: v1alpha1.ObjectReference{Name: "lead-agent"},
			},
			Members: []v1alpha1.TeamMemberSpec{
				{
					Name:     "worker",
					AgentRef: v1alpha1.ObjectReference{Name: "worker-agent"},
				},
			},
		},
	}

	ctx, cancel := testContext()
	defer cancel()

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("failed to create AgentTeam: %v", err)
	}

	key := types.NamespacedName{Name: "status-team", Namespace: ns}

	// Wait for the reconciler to add the finalizer.
	waitFor(t, testTimeout, func() bool {
		var fetched v1alpha1.AgentTeam
		if err := k8sClient.Get(ctx, key, &fetched); err != nil {
			return false
		}
		for _, f := range fetched.Finalizers {
			if f == "agentscope.io/team-finalizer" {
				return true
			}
		}
		return false
	})

	// Wait for the reconciler to transition status.Phase to Running
	// (the legacy path sets Phase=Running on the second reconcile after
	// the finalizer is added).
	waitFor(t, testTimeout, func() bool {
		var fetched v1alpha1.AgentTeam
		if err := k8sClient.Get(ctx, key, &fetched); err != nil {
			return false
		}
		return fetched.Status.Phase == v1alpha1.TeamPhaseRunning
	})

	// Fetch the final state and verify status fields.
	var result v1alpha1.AgentTeam
	if err := k8sClient.Get(ctx, key, &result); err != nil {
		t.Fatalf("failed to get AgentTeam: %v", err)
	}

	if result.Status.Phase != v1alpha1.TeamPhaseRunning {
		t.Errorf("expected phase Running, got %q", result.Status.Phase)
	}
	if result.Status.Lead == nil {
		t.Fatal("expected lead status to be set")
	}
	if result.Status.Lead.AgentRef != "lead-agent" {
		t.Errorf("expected lead agentRef 'lead-agent', got %q", result.Status.Lead.AgentRef)
	}
	if len(result.Status.Members) != 1 {
		t.Fatalf("expected 1 member in status, got %d", len(result.Status.Members))
	}
	if result.Status.Members[0].Name != "worker" {
		t.Errorf("expected member name 'worker', got %q", result.Status.Members[0].Name)
	}
	if result.Status.StartedAt == "" {
		t.Error("expected startedAt to be set")
	}

	// Verify the Ready condition is set.
	foundReady := false
	for _, c := range result.Status.Conditions {
		if c.Type == v1alpha1.ConditionReady && c.Status == metav1.ConditionTrue {
			foundReady = true
			break
		}
	}
	if !foundReady {
		t.Error("expected Ready=True condition to be set")
	}
}

func TestAgentTeamFinalizerAdded(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "team-finalizer")

	team := &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "finalizer-team",
			Namespace: ns,
		},
		Spec: v1alpha1.AgentTeamSpec{
			Objective: "Test finalizer injection",
			Lead: v1alpha1.TeamLeadSpec{
				AgentRef: v1alpha1.ObjectReference{Name: "lead-agent"},
			},
		},
	}

	ctx, cancel := testContext()
	defer cancel()

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("failed to create AgentTeam: %v", err)
	}

	key := types.NamespacedName{Name: "finalizer-team", Namespace: ns}

	// The reconciler should add the team finalizer automatically.
	waitFor(t, testTimeout, func() bool {
		var fetched v1alpha1.AgentTeam
		if err := k8sClient.Get(ctx, key, &fetched); err != nil {
			return false
		}
		for _, f := range fetched.Finalizers {
			if f == "agentscope.io/team-finalizer" {
				return true
			}
		}
		return false
	})
}

func TestAgentTeamDeletion(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "team-delete")
	ctx, cancel := testContext()
	defer cancel()

	team := &v1alpha1.AgentTeam{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deletable-team",
			Namespace: ns,
		},
		Spec: v1alpha1.AgentTeamSpec{
			Objective: "To be deleted",
			Lead: v1alpha1.TeamLeadSpec{
				AgentRef: v1alpha1.ObjectReference{Name: "lead-agent"},
			},
		},
	}

	if err := k8sClient.Create(ctx, team); err != nil {
		t.Fatalf("failed to create AgentTeam: %v", err)
	}

	key := types.NamespacedName{Name: "deletable-team", Namespace: ns}

	// Wait for the reconciler to add the finalizer and transition to Running.
	waitFor(t, testTimeout, func() bool {
		var fetched v1alpha1.AgentTeam
		if err := k8sClient.Get(ctx, key, &fetched); err != nil {
			return false
		}
		return fetched.Status.Phase == v1alpha1.TeamPhaseRunning
	})

	// Create child TeamTask and TeamMessage with the team label.
	task := &v1alpha1.TeamTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "task-1",
			Namespace: ns,
			Labels:    map[string]string{"agentscope.io/team": "deletable-team"},
		},
		Spec: v1alpha1.TeamTaskSpec{
			TeamRef: "deletable-team",
			Subject: "Research topic",
		},
	}
	if err := k8sClient.Create(ctx, task); err != nil {
		t.Fatalf("failed to create TeamTask: %v", err)
	}

	msg := &v1alpha1.TeamMessage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "msg-1",
			Namespace: ns,
			Labels:    map[string]string{"agentscope.io/team": "deletable-team"},
		},
		Spec: v1alpha1.TeamMessageSpec{
			TeamRef: "deletable-team",
			From:    "lead",
			Content: "start working",
		},
	}
	if err := k8sClient.Create(ctx, msg); err != nil {
		t.Fatalf("failed to create TeamMessage: %v", err)
	}

	// Delete the team. The controller's finalizer handles cleanup.
	var toDelete v1alpha1.AgentTeam
	if err := k8sClient.Get(ctx, key, &toDelete); err != nil {
		t.Fatalf("failed to get team for deletion: %v", err)
	}
	if err := k8sClient.Delete(ctx, &toDelete); err != nil {
		t.Fatalf("failed to delete AgentTeam: %v", err)
	}

	// Wait for the team to be fully removed (the controller processes the
	// finalizer, cleans up child resources, then allows deletion).
	waitFor(t, testTimeout, func() bool {
		var gone v1alpha1.AgentTeam
		err := k8sClient.Get(ctx, key, &gone)
		return errors.IsNotFound(err)
	})

	// Verify child TeamTasks were cleaned up by the finalizer.
	var taskList v1alpha1.TeamTaskList
	if err := k8sClient.List(ctx, &taskList, client.InNamespace(ns),
		client.MatchingLabels{"agentscope.io/team": "deletable-team"}); err != nil {
		t.Fatalf("failed to list tasks: %v", err)
	}
	if len(taskList.Items) != 0 {
		t.Errorf("expected 0 tasks after cascade cleanup, got %d", len(taskList.Items))
	}

	// Verify child TeamMessages were cleaned up by the finalizer.
	var msgList v1alpha1.TeamMessageList
	if err := k8sClient.List(ctx, &msgList, client.InNamespace(ns),
		client.MatchingLabels{"agentscope.io/team": "deletable-team"}); err != nil {
		t.Fatalf("failed to list messages: %v", err)
	}
	if len(msgList.Items) != 0 {
		t.Errorf("expected 0 messages after cascade cleanup, got %d", len(msgList.Items))
	}
}

func TestTeamTaskOCC(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "task-occ")
	ctx, cancel := testContext()
	defer cancel()

	task := &v1alpha1.TeamTask{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "contested-task",
			Namespace: ns,
			Labels:    map[string]string{"agentscope.io/team": "occ-team"},
		},
		Spec: v1alpha1.TeamTaskSpec{
			TeamRef: "occ-team",
			Subject: "Contested work item",
		},
	}

	if err := k8sClient.Create(ctx, task); err != nil {
		t.Fatalf("failed to create TeamTask: %v", err)
	}

	// Read two copies with the same resourceVersion to simulate concurrent claims
	var copy1, copy2 v1alpha1.TeamTask
	key := types.NamespacedName{Name: "contested-task", Namespace: ns}
	if err := k8sClient.Get(ctx, key, &copy1); err != nil {
		t.Fatalf("failed to get copy1: %v", err)
	}
	if err := k8sClient.Get(ctx, key, &copy2); err != nil {
		t.Fatalf("failed to get copy2: %v", err)
	}

	// First claim: update status on copy1
	copy1.Status.State = v1alpha1.TeamTaskStateInProgress
	copy1.Status.Owner = "agent-a"
	if err := k8sClient.Status().Update(ctx, &copy1); err != nil {
		t.Fatalf("first claim should succeed: %v", err)
	}

	// Second claim: update status on copy2 (stale resourceVersion)
	copy2.Status.State = v1alpha1.TeamTaskStateInProgress
	copy2.Status.Owner = "agent-b"
	err := k8sClient.Status().Update(ctx, &copy2)
	if err == nil {
		t.Fatal("expected conflict error on stale update, but got nil")
	}
	if !errors.IsConflict(err) {
		t.Errorf("expected Conflict error, got: %v", err)
	}

	// Verify the winner
	var result v1alpha1.TeamTask
	if err := k8sClient.Get(ctx, key, &result); err != nil {
		t.Fatalf("failed to get final state: %v", err)
	}
	if result.Status.Owner != "agent-a" {
		t.Errorf("expected owner 'agent-a', got %q", result.Status.Owner)
	}
	if result.Status.State != v1alpha1.TeamTaskStateInProgress {
		t.Errorf("expected state 'in_progress', got %q", result.Status.State)
	}
}

func TestTeamMessageLifecycle(t *testing.T) {
	skipIfNoEnvtest(t)
	ns := createNamespace(t, "msg-lifecycle")
	ctx, cancel := testContext()
	defer cancel()

	msg := &v1alpha1.TeamMessage{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "lifecycle-msg",
			Namespace: ns,
			Labels:    map[string]string{"agentscope.io/team": "msg-team"},
		},
		Spec: v1alpha1.TeamMessageSpec{
			TeamRef: "msg-team",
			From:    "lead",
			To:      "researcher",
			Content: "Please start the analysis",
			Kind:    "message",
		},
	}

	// Create the message
	if err := k8sClient.Create(ctx, msg); err != nil {
		t.Fatalf("failed to create TeamMessage: %v", err)
	}

	// Verify it starts undelivered
	var fetched v1alpha1.TeamMessage
	key := types.NamespacedName{Name: "lifecycle-msg", Namespace: ns}
	if err := k8sClient.Get(ctx, key, &fetched); err != nil {
		t.Fatalf("failed to get TeamMessage: %v", err)
	}
	if fetched.Status.Delivered {
		t.Error("expected message to start as undelivered")
	}

	// Mark as delivered via status update
	fetched.Status.Delivered = true
	fetched.Status.DeliveredAt = time.Now().UTC().Format(time.RFC3339)
	fetched.Status.Attempts = 1
	if err := k8sClient.Status().Update(ctx, &fetched); err != nil {
		t.Fatalf("failed to update message status: %v", err)
	}

	// Verify delivery persisted
	var updated v1alpha1.TeamMessage
	if err := k8sClient.Get(ctx, key, &updated); err != nil {
		t.Fatalf("failed to get updated TeamMessage: %v", err)
	}
	if !updated.Status.Delivered {
		t.Error("expected message to be marked as delivered")
	}
	if updated.Status.DeliveredAt == "" {
		t.Error("expected deliveredAt to be set")
	}
	if updated.Status.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", updated.Status.Attempts)
	}
}
