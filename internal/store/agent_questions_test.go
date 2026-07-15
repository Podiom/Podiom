package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func openQuestionStore(t *testing.T) *Store {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "podiom.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestAgentQuestionLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openQuestionStore(t)

	created, err := db.CreateAgentQuestion(ctx, AgentQuestion{
		Origin:    AgentQuestionSchedule,
		RefID:     "nightly-audit",
		SessionID: "sess-1",
		Questions: []AgentQuestionItem{
			{ID: "q1", Header: "Scope", Question: "Which package should I audit?", Options: []AgentQuestionOption{{Label: "api"}, {Label: "web"}}},
		},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" || created.Status != AgentQuestionPending {
		t.Fatalf("created = %+v, want an id and pending status", created)
	}
	if len(created.Questions) != 1 || created.Questions[0].ID != "q1" {
		t.Fatalf("questions round-trip failed: %+v", created.Questions)
	}

	pending, err := db.PendingAgentQuestion(ctx, AgentQuestionSchedule, "nightly-audit")
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if pending.ID != created.ID {
		t.Fatalf("pending = %s, want %s", pending.ID, created.ID)
	}

	// A different ref has no pending question.
	if _, err := db.PendingAgentQuestion(ctx, AgentQuestionSchedule, "other"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending for other ref err = %v, want ErrNotFound", err)
	}

	answered, err := db.AnswerAgentQuestion(ctx, created.ID, map[string][]string{"q1": {"api"}})
	if err != nil {
		t.Fatalf("answer: %v", err)
	}
	if answered.Status != AgentQuestionAnswered || answered.AnsweredAt == "" {
		t.Fatalf("answered = %+v, want answered status and timestamp", answered)
	}
	if got := answered.Answers["q1"]; len(got) != 1 || got[0] != "api" {
		t.Fatalf("answers = %+v, want [api]", answered.Answers)
	}

	// Answering again fails: no longer pending.
	if _, err := db.AnswerAgentQuestion(ctx, created.ID, map[string][]string{"q1": {"web"}}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("re-answer err = %v, want ErrNotFound", err)
	}

	// Answered questions are retrievable for the next run.
	list, err := db.ListAnsweredAgentQuestions(ctx, AgentQuestionSchedule, "nightly-audit", 10)
	if err != nil {
		t.Fatalf("list answered: %v", err)
	}
	if len(list) != 1 || list[0].ID != created.ID {
		t.Fatalf("answered list = %+v, want the one question", list)
	}

	// After answering, there is no pending question left.
	if _, err := db.PendingAgentQuestion(ctx, AgentQuestionSchedule, "nightly-audit"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending after answer err = %v, want ErrNotFound", err)
	}

	if err := db.DeleteAgentQuestions(ctx, AgentQuestionSchedule, "nightly-audit"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := db.GetAgentQuestion(ctx, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err = %v, want ErrNotFound", err)
	}
}

func TestPendingGoalQuestionSuppressesDueReviews(t *testing.T) {
	ctx := context.Background()
	db := openQuestionStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "g", LeadAgent: "atlas", ReviewEvery: "24h"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	const past = "2000-01-01T00:00:00Z"
	if err := db.SetGoalNextReview(ctx, goal.ID, past); err != nil {
		t.Fatalf("set next review: %v", err)
	}
	now := "2100-01-01T00:00:00Z"
	if due, _ := db.ListDueGoalReviews(ctx, now); len(due) != 1 {
		t.Fatalf("goal should be due before a question, got %+v", due)
	}

	q, err := db.CreateAgentQuestion(ctx, AgentQuestion{
		Origin:    AgentQuestionGoal,
		RefID:     goal.ID,
		SessionID: "sess-1",
		Questions: []AgentQuestionItem{{ID: "q1", Question: "Proceed?"}},
	})
	if err != nil {
		t.Fatalf("create question: %v", err)
	}
	if due, _ := db.ListDueGoalReviews(ctx, now); len(due) != 0 {
		t.Fatalf("pending goal question should suppress due review, got %+v", due)
	}

	if _, err := db.AnswerAgentQuestion(ctx, q.ID, map[string][]string{"q1": {"yes"}}); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if due, _ := db.ListDueGoalReviews(ctx, now); len(due) != 1 {
		t.Fatalf("answered question should let reviews resume, got %+v", due)
	}
}
