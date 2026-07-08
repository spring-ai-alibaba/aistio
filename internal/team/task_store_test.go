package team

import "testing"

const testNS = "default"

func TestTaskStoreCreate(t *testing.T) {
	store := NewTaskStore()
	task, err := store.Create(testNS, "team1", "review code", "check for bugs", nil)
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == "" {
		t.Error("task ID should not be empty")
	}
	if task.State != TaskStatePending {
		t.Errorf("expected pending, got %s", task.State)
	}
	if task.Subject != "review code" {
		t.Errorf("expected 'review code', got %s", task.Subject)
	}
}

func TestTaskStoreClaim(t *testing.T) {
	store := NewTaskStore()
	task, _ := store.Create(testNS, "team1", "review", "", nil)

	claimed, err := store.Claim(testNS, "team1", task.ID, "member-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.State != TaskStateInProgress {
		t.Errorf("expected in_progress, got %s", claimed.State)
	}
	if claimed.Owner != "member-1" {
		t.Errorf("expected member-1, got %s", claimed.Owner)
	}
}

func TestTaskStoreClaimConflict(t *testing.T) {
	store := NewTaskStore()
	task, _ := store.Create(testNS, "team1", "review", "", nil)

	store.Claim(testNS, "team1", task.ID, "member-1", 0)

	_, err := store.Claim(testNS, "team1", task.ID, "member-2", 0)
	if err == nil {
		t.Error("expected error on double claim")
	}
}

func TestTaskStoreComplete(t *testing.T) {
	store := NewTaskStore()
	task, _ := store.Create(testNS, "team1", "review", "", nil)
	store.Claim(testNS, "team1", task.ID, "member-1", 0)

	completed, err := store.Complete(testNS, "team1", task.ID, "all good")
	if err != nil {
		t.Fatal(err)
	}
	if completed.State != TaskStateCompleted {
		t.Errorf("expected completed, got %s", completed.State)
	}
	if completed.Result != "all good" {
		t.Errorf("expected 'all good', got %s", completed.Result)
	}
}

func TestTaskStoreUnclaim(t *testing.T) {
	store := NewTaskStore()
	task, _ := store.Create(testNS, "team1", "review", "", nil)
	store.Claim(testNS, "team1", task.ID, "member-1", 0)

	unclaimed, err := store.Unclaim(testNS, "team1", task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unclaimed.State != TaskStatePending {
		t.Errorf("expected pending, got %s", unclaimed.State)
	}
	if unclaimed.Owner != "" {
		t.Errorf("expected empty owner, got %s", unclaimed.Owner)
	}
}

func TestTaskStoreGetSummary(t *testing.T) {
	store := NewTaskStore()
	store.Create(testNS, "team1", "task1", "", nil)
	store.Create(testNS, "team1", "task2", "", nil)
	t3, _ := store.Create(testNS, "team1", "task3", "", nil)
	store.Claim(testNS, "team1", t3.ID, "m1", 0)
	store.Complete(testNS, "team1", t3.ID, "done")

	total, pending, _, completed := store.GetSummary(testNS, "team1")
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
	if pending != 2 {
		t.Errorf("expected pending 2, got %d", pending)
	}
	if completed != 1 {
		t.Errorf("expected completed 1, got %d", completed)
	}
}

func TestTaskStoreBlockedBy(t *testing.T) {
	store := NewTaskStore()
	t1, _ := store.Create(testNS, "team1", "setup", "", nil)
	store.Create(testNS, "team1", "deploy", "", []string{t1.ID})

	unblocked := store.GetUnblockedPending(testNS, "team1")
	if len(unblocked) != 1 {
		t.Fatalf("expected 1 unblocked, got %d", len(unblocked))
	}
	if unblocked[0].Subject != "setup" {
		t.Errorf("expected 'setup', got %s", unblocked[0].Subject)
	}
}

func TestTaskStoreDeleteTeam(t *testing.T) {
	store := NewTaskStore()
	store.Create(testNS, "team1", "task1", "", nil)
	store.DeleteTeam(testNS, "team1")

	tasks := store.List(testNS, "team1")
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestTaskStoreOCC(t *testing.T) {
	store := NewTaskStore()
	task, _ := store.Create(testNS, "team1", "task", "", nil)

	_, err := store.Claim(testNS, "team1", task.ID, "m1", 99)
	if err == nil {
		t.Error("expected conflict error for wrong version")
	}
}
