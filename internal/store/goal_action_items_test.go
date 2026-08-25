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
	if _, err := db.UpdateGoalFeedbackBody(ctx, goal.ID, feedback.ID, "edited"); err != nil {
		t.Fatalf("edit unread feedback: %v", err)
	}
}

// Migration 41 adds goal_events.pinned without touching the append-only trigger,
// relying on the trigger listing only the columns that must stay equal and gating
// the whole exemption on kind = 'user_feedback'. That is subtle enough to be
// worth pinning down: a pin toggle must work on feedback and abort on every other
// kind, and both properties must survive any later table rebuild.
func TestGoalFeedbackPinTogglesOnlyOnFeedback(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	feedback, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventUserFeedback, Body: "Never touch production DNS"})
	if err != nil {
		t.Fatalf("append feedback: %v", err)
	}
	if feedback.Pinned {
		t.Fatal("a new feedback note must not start pinned")
	}

	pinned, err := db.SetGoalFeedbackPin(ctx, goal.ID, feedback.ID, true)
	if err != nil {
		t.Fatalf("pin feedback: %v", err)
	}
	if !pinned.Pinned {
		t.Fatal("pinned feedback did not read back as pinned")
	}

	// A pinned note stays editable for the goal's whole life; an ordinary one
	// locks once a review has assembled it into a prompt.
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventReviewStarted}); err != nil {
		t.Fatalf("append review started: %v", err)
	}
	if _, err := db.UpdateGoalFeedbackBody(ctx, goal.ID, feedback.ID, "Never touch production DNS or the CDN"); err != nil {
		t.Fatalf("edit pinned feedback after review: %v", err)
	}
	ordinary, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventUserFeedback, Body: "check CI"})
	if err != nil {
		t.Fatalf("append ordinary feedback: %v", err)
	}
	if _, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventReviewStarted}); err != nil {
		t.Fatalf("append second review started: %v", err)
	}
	if _, err := db.UpdateGoalFeedbackBody(ctx, goal.ID, ordinary.ID, "too late"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("edit read ordinary feedback err = %v, want ErrNotFound", err)
	}

	// The trigger must still refuse a pin on anything that is not feedback.
	progress, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventProgress, Body: "shipped"})
	if err != nil {
		t.Fatalf("append progress: %v", err)
	}
	if _, err := db.db.ExecContext(ctx, `UPDATE goal_events SET pinned = 1 WHERE id = ?`, progress.ID); err == nil {
		t.Fatal("pinning a progress event succeeded; the append-only trigger should have aborted it")
	}
	if _, err := db.SetGoalFeedbackPin(ctx, goal.ID, progress.ID, true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pin non-feedback event err = %v, want ErrNotFound", err)
	}
}

// The two prompt-facing reads must partition a goal's feedback: directives are
// unbounded and oldest-first, ordinary notes are newest-first and must not spend
// their window on notes that have already escaped it.
func TestPinnedAndUnpinnedFeedbackPartition(t *testing.T) {
	ctx := context.Background()
	db := openGoalStore(t)

	goal, err := db.CreateGoal(ctx, Goal{Title: "Launch", LeadAgent: "atlas"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	var ids []int64
	for _, body := range []string{"first", "second", "third", "fourth"} {
		ev, err := db.AppendGoalEvent(ctx, GoalEvent{GoalID: goal.ID, Kind: GoalEventUserFeedback, Body: body})
		if err != nil {
			t.Fatalf("append %q: %v", body, err)
		}
		ids = append(ids, ev.ID)
	}
	for _, id := range []int64{ids[0], ids[2]} {
		if _, err := db.SetGoalFeedbackPin(ctx, goal.ID, id, true); err != nil {
			t.Fatalf("pin %d: %v", id, err)
		}
	}

	directives, err := db.ListPinnedGoalFeedback(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list pinned: %v", err)
	}
	if len(directives) != 2 || directives[0].Body != "first" || directives[1].Body != "third" {
		t.Fatalf("directives = %+v, want first then third (oldest first)", directives)
	}

	ordinary, err := db.ListUnpinnedGoalFeedback(ctx, goal.ID, 10)
	if err != nil {
		t.Fatalf("list unpinned: %v", err)
	}
	if len(ordinary) != 2 || ordinary[0].Body != "fourth" || ordinary[1].Body != "second" {
		t.Fatalf("ordinary = %+v, want fourth then second (newest first, pinned excluded)", ordinary)
	}

	// Unpinning returns a note to the ordinary stream.
	if _, err := db.SetGoalFeedbackPin(ctx, goal.ID, ids[0], false); err != nil {
		t.Fatalf("unpin: %v", err)
	}
	directives, err = db.ListPinnedGoalFeedback(ctx, goal.ID)
	if err != nil {
		t.Fatalf("list pinned after unpin: %v", err)
	}
	if len(directives) != 1 || directives[0].Body != "third" {
		t.Fatalf("directives after unpin = %+v", directives)
	}
	ordinary, err = db.ListUnpinnedGoalFeedback(ctx, goal.ID, 10)
	if err != nil {
		t.Fatalf("list unpinned after unpin: %v", err)
	}
	if len(ordinary) != 3 {
		t.Fatalf("ordinary after unpin = %+v, want 3", ordinary)
	}
}
