package core

import (
	"context"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/projects"
	"github.com/Podiom/Podiom/internal/store"
)

// TestDeleteProjectOrphansTasksAndSessions verifies that deleting a project
// forgets the ledger record while leaving its tasks and sessions in place
// (orphaned), and reports accurate orphan counts.
func TestDeleteProjectOrphansTasksAndSessions(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.CreateAgent(ctx, CreateAgentRequest{Name: "jared", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	project, err := c.CreateProject(ctx, projects.Project{ID: "demo", Name: "Demo"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	task, err := c.CreateTask(ctx, store.Task{Title: "wire it up", AssignedAgent: "jared", ProjectID: project.ID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	sess, err := c.StartTask(ctx, StartTaskRequest{TaskID: task.ID})
	if err != nil {
		t.Fatalf("start task: %v", err)
	}
	if sess.ProjectID != project.ID {
		t.Fatalf("session project id = %q, want %q", sess.ProjectID, project.ID)
	}

	result, err := c.DeleteProject(ctx, project.ID)
	if err != nil {
		t.Fatalf("delete project: %v", err)
	}
	if result.Deleted != project.ID {
		t.Fatalf("deleted = %q, want %q", result.Deleted, project.ID)
	}
	if result.OrphanedTasks != 1 || result.OrphanedSessions != 1 {
		t.Fatalf("orphan counts = %d tasks / %d sessions, want 1/1", result.OrphanedTasks, result.OrphanedSessions)
	}

	// The project record is gone.
	if _, err := c.GetProject(ctx, project.ID); err == nil {
		t.Fatal("project should be deleted from the ledger")
	}
	// The task and session live on, still pointing at the missing project.
	if got, err := c.store.GetTask(ctx, task.ID); err != nil {
		t.Fatalf("task should be preserved after project delete: %v", err)
	} else if got.ProjectID != project.ID {
		t.Fatalf("orphaned task project id = %q, want %q", got.ProjectID, project.ID)
	}
	if got, err := c.store.GetSession(ctx, sess.ID); err != nil {
		t.Fatalf("session should be preserved after project delete: %v", err)
	} else if got.ProjectID != project.ID {
		t.Fatalf("orphaned session project id = %q, want %q", got.ProjectID, project.ID)
	}
}

// TestDeleteProjectMissing verifies deleting an unknown project errors.
func TestDeleteProjectMissing(t *testing.T) {
	ctx := context.Background()
	c, cleanup := newTestCore(t)
	defer cleanup()

	if _, err := c.DeleteProject(ctx, "nope"); err == nil {
		t.Fatal("expected error deleting unknown project")
	}
}
