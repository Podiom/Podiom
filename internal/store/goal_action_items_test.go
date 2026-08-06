package store

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestGoalActionItemLifecycle(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch Podiom", LeadAgent: "atlas", ReviewEvery: "24h"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	created, err := db.CreateGoalActionItem(ctx, GoalActionItem{
		GoalID:       goal.ID,
		SessionID:    "sess-1",
		RunID:        "run-1",
		AgentName:    "atlas",
		Title:        "Post the launch thread on r/selfhosted",
		Instructions: "Title: “I built Podiom”. Body below. Post 14:00–17:00 UTC.",
		Why:          "I have no Reddit account and karma gating blocks new ones.",
	})
	if err != nil {
		t.Fatalf("create action item: %v", err)
	}
	if created.ID == "" || created.Status != GoalActionOpen {
		t.Fatalf("created = %+v, want an id and open status", created)
	}
	if created.RespondedAt != "" {
		t.Fatalf("responded_at = %q, want empty on a fresh item", created.RespondedAt)
	}
	if created.Instructions == "" || created.Why == "" {
		t.Fatalf("instructions/why did not round-trip: %+v", created)
	}

	open, err := db.ListOpenGoalActionItems(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 1 || open[0].ID != created.ID {
		t.Fatalf("open = %+v, want just the new item", open)
	}
	if n, err := db.CountOpenGoalActionItems(ctx, goal.ID); err != nil || n != 1 {
		t.Fatalf("count open = %d, %v; want 1, nil", n, err)
	}
	if responded, err := db.ListRespondedGoalActionItems(ctx, goal.ID, 10); err != nil || len(responded) != 0 {
		t.Fatalf("responded = %+v, %v; want empty", responded, err)
	}

	answered, err := db.RespondGoalActionItem(ctx, created.ID, GoalActionDone, "Posted it — 340 upvotes, https://redd.it/x")
	if err != nil {
		t.Fatalf("respond: %v", err)
	}
	if answered.Status != GoalActionDone || answered.RespondedAt == "" {
		t.Fatalf("answered = %+v, want done status and a timestamp", answered)
	}

	// A verdict is given exactly once: a second response must not quietly
	// rewrite what the agent may already have read.
	if _, err := db.RespondGoalActionItem(ctx, created.ID, GoalActionDeclined, "changed my mind"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second respond err = %v, want ErrNotFound", err)
	}

	if open, err = db.ListOpenGoalActionItems(ctx, goal.ID); err != nil || len(open) != 0 {
		t.Fatalf("open after respond = %+v, %v; want empty", open, err)
	}
	responded, err := db.ListRespondedGoalActionItems(ctx, goal.ID, 10)
	if err != nil {
		t.Fatalf("list responded: %v", err)
	}
	if len(responded) != 1 || responded[0].Response == "" {
		t.Fatalf("responded = %+v, want the answered item with its note", responded)
	}
}

// Open items are the review prompt's nag list, so their order must be stable and
// oldest-first — the longest-waiting ask leads the carousel too.
func TestGoalActionItemsListOldestFirst(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	for i, title := range []string{"first", "second", "third"} {
		if _, err := db.CreateGoalActionItem(ctx, GoalActionItem{GoalID: goal.ID, Title: title}); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
		// created_at has one-second resolution; age the rows apart so ordering is
		// proven by time rather than by the insertion tiebreaker alone.
		if _, err := db.db.ExecContext(ctx,
			`UPDATE goal_action_items SET created_at = datetime('now', ?) WHERE title = ?`,
			fmt.Sprintf("-%d hours", 5-i), title); err != nil {
			t.Fatalf("age %s: %v", title, err)
		}
	}
	open, err := db.ListOpenGoalActionItems(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != 3 || open[0].Title != "first" || open[2].Title != "third" {
		t.Fatalf("open order = %+v, want first, second, third", open)
	}
}

// Items filed in the same second (a single review can file several) must still
// come back in the order they were filed. Ids are uuids, so only the insertion
// tiebreaker makes "oldest first" mean anything at this resolution.
func TestGoalActionItemsSameSecondKeepInsertionOrder(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	titles := []string{"post the thread", "sign the contract", "call the provider"}
	for _, title := range titles {
		if _, err := db.CreateGoalActionItem(ctx, GoalActionItem{GoalID: goal.ID, Title: title}); err != nil {
			t.Fatalf("create %s: %v", title, err)
		}
	}
	open, err := db.ListOpenGoalActionItems(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list open: %v", err)
	}
	if len(open) != len(titles) {
		t.Fatalf("open = %d items, want %d", len(open), len(titles))
	}
	for i, want := range titles {
		if open[i].Title != want {
			t.Fatalf("open[%d] = %q, want %q (full order: %+v)", i, open[i].Title, want, open)
		}
	}
}

// Action items belong to their goal: deleting the goal takes them with it, which
// is why the table carries a FK cascade and needs no explicit cleanup path.
func TestGoalActionItemsCascadeOnGoalDelete(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	item, err := db.CreateGoalActionItem(ctx, GoalActionItem{GoalID: goal.ID, Title: "Post the thread"})
	if err != nil {
		t.Fatalf("create item: %v", err)
	}
	if err := db.DeleteGoal(ctx, goal.ID); err != nil {
		t.Fatalf("delete goal: %v", err)
	}
	if _, err := db.GetGoalActionItem(ctx, item.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get item after goal delete err = %v, want ErrNotFound", err)
	}
}

// Migration 31 widened goal_events' kind CHECK and rebuilt the table. Both new
// kinds must be accepted, and the rebuild must have carried the append-only
// trigger (and its user_feedback exemption) across intact.
func TestGoalActionEventsAppendOnlyAfterRebuild(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	asked, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventActionRequested, Body: "Needs you: post the thread"})
	if err != nil {
		t.Fatalf("append action_requested: %v", err)
	}
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventActionResponded, Body: "Done — posted"}); err != nil {
		t.Fatalf("append action_responded: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE goal_events SET body = 'tampered' WHERE id = ?`, asked.ID); err == nil {
		t.Fatal("updating an action event succeeded; the append-only trigger did not survive the rebuild")
	}

	// The one sanctioned update — an unread feedback body — must still work.
	feedback, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventUserFeedback, Body: "original"})
	if err != nil {
		t.Fatalf("append feedback: %v", err)
	}
	if _, err := db.UpdateUnreadGoalFeedback(ctx, goal.ID, feedback.ID, "edited"); err != nil {
		t.Fatalf("edit unread feedback: %v", err)
	}
}
