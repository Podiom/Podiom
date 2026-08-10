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

	created, err := db.CreateTask(ctx, Task{ProjectID: "mc", Title: "Add dark mode", AssignedAgent: "jared", PlanRequired: true, GoalID: "goal-1"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if created.Status != TaskBacklog {
		t.Fatalf("default status = %q, want backlog", created.Status)
	}
	if !created.PlanRequired {
		t.Fatalf("plan_required did not round-trip on create")
	}
	if created.GoalID != "goal-1" {
		t.Fatalf("goal_id did not round-trip on create: %q", created.GoalID)
	}

	created.Status = TaskInProgress
	created.PlanRequired = false
	created.GoalID = ""
	updated, err := db.UpdateTask(ctx, created)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.Status != TaskInProgress {
		t.Fatalf("status not updated: %+v", updated)
	}
	if updated.PlanRequired {
		t.Fatalf("plan_required did not round-trip on update")
	}
	if updated.GoalID != "" {
		t.Fatalf("goal_id did not clear on update: %q", updated.GoalID)
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

// TestTaskCreatorProvenance pins the two decisions behind created_by_*: an agent
// session's authorship round-trips on create, and an update can never rewrite it
// (otherwise a later agent could claim a task the user made).
func TestTaskCreatorProvenance(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()

	agentMade, err := db.CreateTask(ctx, Task{
		Title:            "Benchmark the candidates",
		CreatedBySession: "sess-1",
		CreatedByAgent:   "jared",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if agentMade.CreatedBySession != "sess-1" || agentMade.CreatedByAgent != "jared" {
		t.Fatalf("provenance did not round-trip on create: %+v", agentMade)
	}

	// A task the user made in the UI carries no attribution rather than a wrong one.
	userMade, err := db.CreateTask(ctx, Task{Title: "Ship the release"})
	if err != nil {
		t.Fatalf("create user task: %v", err)
	}
	if userMade.CreatedBySession != "" || userMade.CreatedByAgent != "" {
		t.Fatalf("user-created task should carry no attribution: %+v", userMade)
	}

	// Authorship is immutable: zeroing the fields on update must not clear them.
	agentMade.CreatedBySession = ""
	agentMade.CreatedByAgent = ""
	agentMade.Title = "Benchmark the three candidates"
	updated, err := db.UpdateTask(ctx, agentMade)
	if err != nil {
		t.Fatalf("update task: %v", err)
	}
	if updated.CreatedBySession != "sess-1" || updated.CreatedByAgent != "jared" {
		t.Fatalf("update rewrote authorship: %+v", updated)
	}

	created, err := db.ListTasksCreatedBySession(ctx, "sess-1")
	if err != nil {
		t.Fatalf("list by session: %v", err)
	}
	if len(created) != 1 || created[0].ID != updated.ID {
		t.Fatalf("expected only the agent-created task, got %+v", created)
	}
	if none, err := db.ListTasksCreatedBySession(ctx, "sess-unknown"); err != nil || len(none) != 0 {
		t.Fatalf("unknown session should list nothing, got %+v (err %v)", none, err)
	}
}
