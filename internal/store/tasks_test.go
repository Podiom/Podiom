package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestTaskCRUDAndDuePickup(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	// An agent is needed only for FK-free fields here; create one to keep
	// assigned_agent realistic in later layers (no FK on tasks).
	if _, err := db.CreateAgent(ctx, Agent{Name: "jared", Provider: "claude", PermissionMode: "approve"}); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	created, err := db.CreateTask(ctx, Task{ProjectID: "mc", Title: "Add dark mode", AssignedAgent: "jared"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if created.Status != TaskBacklog {
		t.Fatalf("default status = %q, want backlog", created.Status)
	}

	created.Status = TaskInProgress
	updated, err := db.UpdateTask(ctx, created)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Status != TaskInProgress {
		t.Fatalf("status not updated: %+v", updated)
	}

	all, err := db.ListTasks(ctx)
	if err != nil || len(all) != 1 {
		t.Fatalf("list tasks: %+v err=%v", all, err)
	}
}

func TestListDueTasksRespectsPickupAndStatus(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	// Due and eligible.
	if _, err := db.CreateTask(ctx, Task{Title: "due", AssignedAgent: "jared", PickupAt: past}); err != nil {
		t.Fatal(err)
	}
	// Not yet due.
	if _, err := db.CreateTask(ctx, Task{Title: "later", AssignedAgent: "jared", PickupAt: future}); err != nil {
		t.Fatal(err)
	}
	// Due but unassigned -> skipped.
	if _, err := db.CreateTask(ctx, Task{Title: "orphan", PickupAt: past}); err != nil {
		t.Fatal(err)
	}

	due, err := db.ListDueTasks(ctx, now)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].Title != "due" {
		t.Fatalf("expected only the due assigned task, got %+v", due)
	}
}
