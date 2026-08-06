package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
)

// fileActionItem posts one action item through the tool endpoint and returns it.
func fileActionItem(t *testing.T, srv *Server, sessionID, title string) store.GoalActionItem {
	t.Helper()
	body, _ := json.Marshal(map[string]string{
		"session_id":   sessionID,
		"agent_name":   "atlas",
		"title":        title,
		"instructions": "Do the thing, then paste the link here.",
		"why":          "Only you have the account.",
	})
	rr := httptest.NewRecorder()
	srv.handleGoalActionItems(rr, httptest.NewRequest(http.MethodPost, "/api/goal-action-items", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("file action item status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		ActionItemID string `json:"action_item_id"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&res); err != nil {
		t.Fatalf("decode file response: %v", err)
	}
	item, err := srv.core.Store().GetGoalActionItem(context.Background(), res.ActionItemID)
	if err != nil {
		t.Fatalf("get filed item: %v", err)
	}
	return item
}

func TestGoalActionItemsFileRespondAndSurfaceOnDetail(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	ctx := context.Background()
	goal := createGoalViaCore(t, srv, store.Goal{Title: "Launch Podiom"})
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	item := fileActionItem(t, srv, sess.ID, "Post the launch thread on r/selfhosted")
	if item.Status != store.GoalActionOpen || item.GoalID != goal.ID {
		t.Fatalf("filed item = %+v, want open on the goal", item)
	}

	// The detail response is what the carousel renders.
	rr := httptest.NewRecorder()
	srv.handleGoalItem(rr, httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID, nil), goal.ID)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var detail GoalDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.ActionItems) != 1 || detail.ActionItems[0].ID != item.ID {
		t.Fatalf("detail action items = %+v, want the filed item", detail.ActionItems)
	}

	// The list response drives the "needs you" triage.
	rr = httptest.NewRecorder()
	srv.handleGoals(rr, httptest.NewRequest(http.MethodGet, "/api/goals", nil))
	var list []struct {
		ID              string `json:"ID"`
		OpenActionItems int    `json:"open_action_items"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	var counted bool
	for _, g := range list {
		if g.ID == goal.ID {
			counted = g.OpenActionItems == 1
		}
	}
	if !counted {
		t.Fatalf("goals list = %+v, want open_action_items 1 on the goal", list)
	}

	// Responding records the verdict and the note.
	respond, _ := json.Marshal(map[string]string{"status": "done", "response": "Posted — https://redd.it/x"})
	rr = httptest.NewRecorder()
	srv.handleGoalActionItem(rr, httptest.NewRequest(http.MethodPost, "/api/goal-action-items/"+item.ID+"/respond", bytes.NewReader(respond)))
	if rr.Code != http.StatusOK {
		t.Fatalf("respond status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var answered store.GoalActionItem
	if err := json.NewDecoder(rr.Body).Decode(&answered); err != nil {
		t.Fatalf("decode respond: %v", err)
	}
	if answered.Status != store.GoalActionDone || !strings.Contains(answered.Response, "redd.it") {
		t.Fatalf("answered = %+v, want done with the note", answered)
	}

	// Both halves of the hand-off are on the timeline.
	events, err := srv.core.ListGoalEvents(ctx, goal.ID, 0, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	kinds := map[store.GoalEventKind]bool{}
	for _, ev := range events {
		kinds[ev.Kind] = true
	}
	if !kinds[store.GoalEventActionRequested] || !kinds[store.GoalEventActionResponded] {
		t.Fatalf("timeline kinds = %v, want both action_requested and action_responded", kinds)
	}
}

func TestGoalActionItemRespondRejectsBadInput(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	ctx := context.Background()
	goal := createGoalViaCore(t, srv, store.Goal{})
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	item := fileActionItem(t, srv, sess.ID, "Post the thread")

	// GET is not a way to answer.
	rr := httptest.NewRecorder()
	srv.handleGoalActionItem(rr, httptest.NewRequest(http.MethodGet, "/api/goal-action-items/"+item.ID+"/respond", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET respond status = %d, want 405", rr.Code)
	}

	// Only /respond exists under an item.
	rr = httptest.NewRecorder()
	srv.handleGoalActionItem(rr, httptest.NewRequest(http.MethodPost, "/api/goal-action-items/"+item.ID+"/delete", bytes.NewBufferString("{}")))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown action status = %d, want 404", rr.Code)
	}

	// "open" is not a verdict the user can give.
	body, _ := json.Marshal(map[string]string{"status": "open"})
	rr = httptest.NewRecorder()
	srv.handleGoalActionItem(rr, httptest.NewRequest(http.MethodPost, "/api/goal-action-items/"+item.ID+"/respond", bytes.NewReader(body)))
	if rr.Code == http.StatusOK {
		t.Fatalf("responding 'open' succeeded; body=%s", rr.Body.String())
	}
}
