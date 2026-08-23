package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/creds"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/store"
	podiomtools "github.com/Podiom/Podiom/internal/tools"
)

func newGoalTestServer(t *testing.T) (config.Paths, *Server, func()) {
	t.Helper()
	paths, srv, cleanup := newAgentAPITestServer(t)
	if _, err := srv.core.CreateAgent(context.Background(), core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return paths, srv, cleanup
}

func createGoalViaCore(t *testing.T, srv *Server, goal store.Goal) store.Goal {
	t.Helper()
	if goal.LeadAgent == "" {
		goal.LeadAgent = "atlas"
	}
	if goal.Title == "" {
		goal.Title = "Ship it"
	}
	created, err := srv.core.CreateGoal(context.Background(), goal)
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return created
}

func TestGoalCreateKicksPlanningSession(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()

	body := `{"title":"Grow the newsletter","description":"to 500 subs","success_criteria":"500 subscribers",` +
		`"metrics":[{"name":"Subscribers","target":500,"current":120}],"review_every":"24h","lead_agent":"atlas"}`
	req := httptest.NewRequest(http.MethodPost, "/api/goals", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.handleGoals(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var goal store.Goal
	if err := json.NewDecoder(rr.Body).Decode(&goal); err != nil {
		t.Fatalf("decode goal: %v", err)
	}
	if goal.Status != store.GoalActive || goal.NextReviewAt == "" {
		t.Fatalf("created goal = %+v, want active with next review set", goal)
	}

	// Planning runs asynchronously against the fake adapter: wait for the
	// planning_started event and its OriginGoal session to land.
	deadline := time.Now().Add(5 * time.Second)
	var planningSeen bool
	for time.Now().Before(deadline) {
		events, err := srv.core.ListGoalEvents(context.Background(), goal.ID, 0, 0)
		if err != nil {
			t.Fatalf("list events: %v", err)
		}
		for _, ev := range events {
			if ev.Kind == store.GoalEventPlanningStarted {
				planningSeen = true
				sess, err := srv.core.GetSession(context.Background(), ev.SessionID)
				if err != nil {
					t.Fatalf("get planning session: %v", err)
				}
				if sess.Origin != store.OriginGoal || sess.GoalID != goal.ID {
					t.Fatalf("planning session = %+v, want origin goal + goal linkage", sess)
				}
			}
		}
		if planningSeen {
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !planningSeen {
		t.Fatalf("planning session never started")
	}
	// Let the background goroutine's post-run bookkeeping settle before the
	// test store closes.
	time.Sleep(200 * time.Millisecond)
}

func TestGoalRunDetailIsBoundToGoalAndTurn(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	ctx := context.Background()
	goal := createGoalViaCore(t, srv, store.Goal{})
	other := createGoalViaCore(t, srv, store.Goal{Title: "Other"})
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{AgentName: "atlas", Origin: store.OriginGoal, GoalID: goal.ID})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	run, err := srv.core.Store().CreateGoalRun(ctx, store.GoalRun{GoalID: goal.ID, SessionID: sess.ID, Kind: store.GoalRunReview, AgentName: "atlas"})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	messages, err := srv.core.Store().AppendMessages(ctx, sess.ID, []store.Message{{Role: store.RoleUser, Content: "review"}, {Role: store.RoleAssistant, Content: "done"}})
	if err != nil {
		t.Fatalf("append messages: %v", err)
	}
	if _, err := srv.core.Store().SetGoalRunTurn(ctx, run.ID, messages[0].ID); err != nil {
		t.Fatalf("set run turn: %v", err)
	}
	if _, err := srv.core.Store().AppendGoalEvent(ctx, store.GoalEvent{GoalID: goal.ID, SessionID: sess.ID, RunID: run.ID, Kind: store.GoalEventProgress, Body: "shipped"}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if _, err := srv.core.Store().FinishGoalRun(ctx, run.ID, store.GoalRunSucceeded, ""); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID+"/runs/"+run.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("run detail status = %d body=%s", rr.Code, rr.Body.String())
	}
	var detail goalRunDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode run detail: %v", err)
	}
	if detail.Run.ID != run.ID || len(detail.Messages) != 2 || len(detail.Events) != 1 || !detail.TranscriptAvailable {
		t.Fatalf("run detail = %+v", detail)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/goals/"+other.ID+"/runs/"+run.ID, nil)
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("cross-goal run status = %d, want 404", rr.Code)
	}
}

func TestGoalRateLimitAPIListsAndResolves(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	ctx := context.Background()

	goal := createGoalViaCore(t, srv, store.Goal{})
	block, err := srv.core.Store().CreateGoalRateLimitBlock(ctx, store.GoalRateLimitBlock{
		GoalID:    goal.ID,
		SessionID: "rate-session",
		Phase:     store.GoalRateLimitReview,
		Provider:  config.ProviderClaude,
		Model:     "sonnet",
		Effort:    "medium",
		Error:     "rate limited on claude/default; no fallback available",
	})
	if err != nil {
		t.Fatalf("create rate-limit block: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/goals", nil)
	rr := httptest.NewRecorder()
	srv.handleGoals(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var items []goalListItem
	if err := json.NewDecoder(rr.Body).Decode(&items); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(items) != 1 || items[0].PendingRateLimit == nil || items[0].PendingRateLimit.ID != block.ID {
		t.Fatalf("list pending rate limit = %+v, want block %s", items, block.ID)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID, nil)
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail status = %d; body=%s", rr.Code, rr.Body.String())
	}
	var detail GoalDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if len(detail.RateLimitBlocks) != 1 || detail.RateLimitBlocks[0].ID != block.ID {
		t.Fatalf("detail rate limits = %+v, want block", detail.RateLimitBlocks)
	}

	body := `{"provider":"codex","model":"gpt-5","effort":"high","retry":false}`
	req = httptest.NewRequest(http.MethodPost, "/api/goal-rate-limits/"+block.ID+"/resolve", bytes.NewBufferString(body))
	rr = httptest.NewRecorder()
	srv.handleGoalRateLimit(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("resolve status = %d; body=%s", rr.Code, rr.Body.String())
	}
	updated, err := srv.core.GetGoal(ctx, goal.ID)
	if err != nil {
		t.Fatalf("get updated goal: %v", err)
	}
	if updated.Provider != config.ProviderCodex || updated.Model != "gpt-5" || updated.Effort != "high" {
		t.Fatalf("resolved goal target = %+v", updated)
	}
	if pending, err := srv.core.PendingGoalRateLimit(ctx, goal.ID); err != nil || pending != nil {
		t.Fatalf("pending after resolve = %+v err=%v, want nil", pending, err)
	}
}

func TestGoalStatusTransitionsAndAgentRestrictions(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{ReviewEvery: "24h"})

	patch := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPatch, "/api/goals/"+goal.ID, bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleGoal(rr, req)
		return rr
	}

	// Agent-stamped status change is forbidden.
	if rr := patch(`{"status":"done","session_id":"sess-1"}`); rr.Code != http.StatusForbidden {
		t.Fatalf("agent status change: status = %d; body=%s", rr.Code, rr.Body.String())
	}
	// Agent-stamped title change is rejected by policy.
	if rr := patch(`{"title":"hijacked","session_id":"sess-1"}`); rr.Code == http.StatusOK {
		t.Fatalf("agent title change should fail")
	}
	// done is only reachable from review.
	if rr := patch(`{"status":"done"}`); rr.Code == http.StatusOK {
		t.Fatalf("active -> done should be rejected")
	}
	// pause clears the review clock; resume restores it.
	if rr := patch(`{"status":"paused"}`); rr.Code != http.StatusOK {
		t.Fatalf("pause: %d %s", rr.Code, rr.Body.String())
	}
	paused, _ := srv.core.GetGoal(context.Background(), goal.ID)
	if paused.NextReviewAt != "" {
		t.Fatalf("paused goal still has next_review_at %q", paused.NextReviewAt)
	}
	if rr := patch(`{"status":"active"}`); rr.Code != http.StatusOK {
		t.Fatalf("resume: %d %s", rr.Code, rr.Body.String())
	}
	resumed, _ := srv.core.GetGoal(context.Background(), goal.ID)
	if resumed.NextReviewAt == "" {
		t.Fatalf("resumed goal has no next_review_at")
	}

	// Propose completion (agent path) → review; then the user marks done.
	if _, err := srv.core.ProposeGoalCompletion(context.Background(), goal.ID, "sess-9", "All criteria met."); err != nil {
		t.Fatalf("propose: %v", err)
	}
	if rr := patch(`{"status":"done"}`); rr.Code != http.StatusOK {
		t.Fatalf("review -> done: %d %s", rr.Code, rr.Body.String())
	}
	done, _ := srv.core.GetGoal(context.Background(), goal.ID)
	if done.Status != store.GoalDone || done.ClosingReport == "" {
		t.Fatalf("done goal = %+v", done)
	}
}

func TestGoalProgressEventsApplyMetrics(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{
		Metrics: []store.GoalMetric{{Name: "Subscribers", Target: 500, Current: 120}},
	})

	body := `{"body":"Issue #3 shipped","kind":"progress","metric_updates":[{"name":"Subscribers","current":180}],"session_id":"sess-2"}`
	req := httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/events", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", rr.Code, rr.Body.String())
	}

	got, _ := srv.core.GetGoal(context.Background(), goal.ID)
	if got.Metrics[0].Current != 180 {
		t.Fatalf("metric current = %v, want 180", got.Metrics[0].Current)
	}
	events, _ := srv.core.ListGoalEvents(context.Background(), goal.ID, 0, 0)
	var kinds []store.GoalEventKind
	for _, ev := range events {
		kinds = append(kinds, ev.Kind)
	}
	// newest first: metric_update, progress, created
	if len(events) != 3 || kinds[0] != store.GoalEventMetricUpdate || kinds[1] != store.GoalEventProgress {
		t.Fatalf("timeline kinds = %v", kinds)
	}
	if events[1].SessionID != "sess-2" {
		t.Fatalf("progress event session = %q, want sess-2", events[1].SessionID)
	}
	// Unknown metric is rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/events",
		bytes.NewBufferString(`{"metric_updates":[{"name":"nope","current":1}]}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("unknown metric should fail")
	}
}

// The next step reaches the goal over the same progress endpoint the agent tools
// use, and a later entry that omits it leaves the stated intent alone.
func TestGoalProgressEventsStateNextStep(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{})

	body := `{"body":"Issue #3 shipped","session_id":"sess-2",` +
		`"next_step":"Post the launch thread on r/selfhosted",` +
		`"next_step_why":"Organic signups stalled."}`
	req := httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/events", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("progress: %d %s", rr.Code, rr.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID, nil)
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("get goal: %d %s", rr.Code, rr.Body.String())
	}
	var detail GoalDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Goal.NextStep != "Post the launch thread on r/selfhosted" {
		t.Fatalf("next step = %q", detail.Goal.NextStep)
	}
	if detail.Goal.NextStepWhy != "Organic signups stalled." || detail.Goal.NextStepAt == "" {
		t.Fatalf("next step rationale/timestamp missing: %+v", detail.Goal)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/events",
		bytes.NewBufferString(`{"body":"Drafted the thread."}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("second progress: %d %s", rr.Code, rr.Body.String())
	}
	got, _ := srv.core.GetGoal(context.Background(), goal.ID)
	if got.NextStep != "Post the launch thread on r/selfhosted" {
		t.Fatalf("omitting next_step changed it to %q", got.NextStep)
	}
}

func TestGoalFeedbackEndpointRecordsUserEventOnly(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{ReviewEvery: "24h"})
	beforeNext := goal.NextReviewAt

	req := httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/feedback", bytes.NewBufferString(`{"body":"  Keep launch scope small.  "}`))
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("feedback: %d %s", rr.Code, rr.Body.String())
	}
	var ev store.GoalEvent
	if err := json.NewDecoder(rr.Body).Decode(&ev); err != nil {
		t.Fatalf("decode feedback event: %v", err)
	}
	if ev.Kind != store.GoalEventUserFeedback || ev.SessionID != "" || ev.Body != "Keep launch scope small." {
		t.Fatalf("feedback event = %+v", ev)
	}
	got, err := srv.core.GetGoal(context.Background(), goal.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if got.Status != store.GoalActive || got.NextReviewAt != beforeNext {
		t.Fatalf("feedback changed lifecycle: before next=%q after=%+v", beforeNext, got)
	}
	events, _ := srv.core.ListGoalEvents(context.Background(), goal.ID, 0, 0)
	if len(events) != 2 || events[0].Kind != store.GoalEventUserFeedback || events[1].Kind != store.GoalEventCreated {
		t.Fatalf("timeline after feedback = %+v", events)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/goals/"+goal.ID+"/feedback",
		bytes.NewBufferString(`{"event_id":`+strconv.FormatInt(ev.ID, 10)+`,"body":"  Keep launch scope tiny.  "}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("edit feedback: %d %s", rr.Code, rr.Body.String())
	}
	var edited store.GoalEvent
	if err := json.NewDecoder(rr.Body).Decode(&edited); err != nil {
		t.Fatalf("decode edited feedback event: %v", err)
	}
	if edited.ID != ev.ID || edited.Body != "Keep launch scope tiny." {
		t.Fatalf("edited feedback = %+v", edited)
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/goals/"+goal.ID+"/feedback", bytes.NewBufferString(`{"body":"hello"}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("edit feedback without event_id should fail")
	}

	req = httptest.NewRequest(http.MethodPatch, "/api/goals/"+goal.ID+"/feedback",
		bytes.NewBufferString(`{"event_id":`+strconv.FormatInt(ev.ID, 10)+`,"body":"  "}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("empty feedback edit should fail")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/goals/"+goal.ID+"/feedback", bytes.NewBufferString(`{"body":"  "}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("empty feedback should fail")
	}

	req = httptest.NewRequest(http.MethodPost, "/api/goals/missing/feedback", bytes.NewBufferString(`{"body":"hello"}`))
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatalf("missing goal feedback should fail")
	}
}

func TestAccessRequestGrantExecution(t *testing.T) {
	paths, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{})

	if err := podiommcp.SaveUserFile(paths.MCPYAML, []podiommcp.Server{{
		Name:      "netlify",
		Transport: podiommcp.TransportHTTP,
		URL:       "http://127.0.0.1:1/mcp",
	}}); err != nil {
		t.Fatalf("save mcp yaml: %v", err)
	}

	file := func(body string) store.AccessRequest {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleAccessRequests(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("file request: %d %s", rr.Code, rr.Body.String())
		}
		var out store.AccessRequest
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	decide := func(id, action, note string) (store.AccessRequest, *httptest.ResponseRecorder) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests/"+id+"/"+action, bytes.NewBufferString(`{"note":`+note+`}`))
		rr := httptest.NewRecorder()
		srv.handleAccessRequest(rr, req)
		var out store.AccessRequest
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return out, rr
	}

	// mcp_server: approve executes the assignment through the shared path.
	mcpReq := file(`{"goal_id":"` + goal.ID + `","kind":"mcp_server","payload":{"server_name":"netlify"},"reason":"deploy access","session_id":"s1","agent_name":"atlas"}`)
	decided, rr := decide(mcpReq.ID, "approve", `"go"`)
	if rr.Code != http.StatusOK || decided.Status != store.AccessExecuted {
		t.Fatalf("mcp grant = %+v (%d %s)", decided, rr.Code, rr.Body.String())
	}
	agent, _ := srv.core.GetAgent(context.Background(), "atlas")
	if len(agent.MCPServers) != 1 || agent.MCPServers[0] != "netlify" {
		t.Fatalf("agent servers = %v, want [netlify]", agent.MCPServers)
	}

	// mcp_server for a server not in the catalogue: grant fails, stays retryable.
	badReq := file(`{"goal_id":"` + goal.ID + `","kind":"mcp_server","payload":{"server_name":"ghost"},"reason":"x","agent_name":"atlas"}`)
	failed, _ := decide(badReq.ID, "approve", `""`)
	if failed.Status != store.AccessFailed || failed.ExecutionError == "" {
		t.Fatalf("bad grant = %+v, want failed with error", failed)
	}
	// Retry after fixing: still fails (server still missing) but the guard
	// allows re-deciding from failed.
	if _, rr := decide(badReq.ID, "approve", `""`); rr.Code != http.StatusOK {
		t.Fatalf("retry from failed: %d %s", rr.Code, rr.Body.String())
	}

	// permission_mode: approve flips the agent's mode.
	pmReq := file(`{"goal_id":"` + goal.ID + `","kind":"permission_mode","payload":{"mode":"yolo"},"reason":"unattended deploys","agent_name":"atlas"}`)
	pmDecided, _ := decide(pmReq.ID, "approve", `""`)
	if pmDecided.Status != store.AccessExecuted {
		t.Fatalf("permission grant = %+v", pmDecided)
	}
	agent, _ = srv.core.GetAgent(context.Background(), "atlas")
	if agent.PermissionMode != config.PermissionYolo {
		t.Fatalf("agent mode = %q, want yolo", agent.PermissionMode)
	}

	// cli_tool: acknowledge-only, approval is terminal.
	cliReq := file(`{"goal_id":"` + goal.ID + `","kind":"cli_tool","payload":{"tool":"lychee","install_hint":"brew install lychee"},"reason":"link checks","agent_name":"atlas"}`)
	cliDecided, _ := decide(cliReq.ID, "approve", `"installed it"`)
	if cliDecided.Status != store.AccessApproved || cliDecided.DecisionNote != "installed it" {
		t.Fatalf("cli grant = %+v", cliDecided)
	}

	// env_var carrying a secret value is rejected outright.
	req := httptest.NewRequest(http.MethodPost, "/api/access-requests",
		bytes.NewBufferString(`{"goal_id":"`+goal.ID+`","kind":"env_var","payload":{"var_name":"TOKEN","value":"hunter2"},"reason":"x"}`))
	rec := httptest.NewRecorder()
	srv.handleAccessRequests(rec, req)
	if rec.Code == http.StatusOK {
		t.Fatalf("secret-bearing env_var request should be rejected")
	}

	// Deny with a note; double-decide rejected.
	envReq := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"TOKEN","purpose":"staging"},"reason":"live examples","agent_name":"atlas"}`)
	denied, _ := decide(envReq.ID, "deny", `"not yet"`)
	if denied.Status != store.AccessDenied || denied.DecisionNote != "not yet" {
		t.Fatalf("denied = %+v", denied)
	}
	if _, rr := decide(envReq.ID, "approve", `""`); rr.Code == http.StatusOK {
		t.Fatalf("double decide should fail")
	}

	// The audit trail bracketed every decision.
	events, _ := srv.core.ListGoalEvents(context.Background(), goal.ID, 0, 0)
	var requested, decidedCount int
	for _, ev := range events {
		switch ev.Kind {
		case store.GoalEventAccessRequested:
			requested++
		case store.GoalEventAccessDecided:
			decidedCount++
		}
	}
	if requested != 5 {
		t.Fatalf("access_requested events = %d, want 5", requested)
	}
	// 5 decisions (mcp ok, bad×2, pm, cli, deny = 6) plus grant-failure entries ×2.
	if decidedCount < 6 {
		t.Fatalf("access_decided events = %d, want >= 6", decidedCount)
	}
}

func TestEnvVarGrantWithSecretValue(t *testing.T) {
	paths, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{})

	file := func(body string) store.AccessRequest {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleAccessRequests(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("file request: %d %s", rr.Code, rr.Body.String())
		}
		var out store.AccessRequest
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	decide := func(id, body string) (store.AccessRequest, *httptest.ResponseRecorder) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests/"+id+"/approve", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleAccessRequest(rr, req)
		var out store.AccessRequest
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return out, rr
	}

	// Approving with a secret value fulfills the request: credential stored,
	// request executed, evidence names the variable but never the value.
	withValue := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"GITHUB_TOKEN","purpose":"gh API"},"reason":"repo access","agent_name":"atlas"}`)
	granted, rr := decide(withValue.ID, `{"note":"here you go","secret_value":"tok_s3cret"}`)
	if rr.Code != http.StatusOK || granted.Status != store.AccessExecuted {
		t.Fatalf("grant = %+v (%d %s)", granted, rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "tok_s3cret") {
		t.Fatalf("decide response leaks the secret: %s", rr.Body.String())
	}

	creds, err := srv.core.ListCredentials(context.Background())
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(creds) != 1 || creds[0].Name != "GITHUB_TOKEN" || creds[0].Value != "tok_s3cret" || creds[0].GoalID != goal.ID {
		t.Fatalf("stored credentials = %+v", creds)
	}
	info, err := os.Stat(paths.CredentialsYAML)
	if err != nil {
		t.Fatalf("stat credentials file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credentials file mode = %v, want 0600", info.Mode().Perm())
	}

	// The goal detail (request rows, timeline, evidence) must never carry the value.
	req := httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID, nil)
	rec := httptest.NewRecorder()
	srv.handleGoal(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "tok_s3cret") {
		t.Fatal("goal detail leaks the secret value")
	}
	if !strings.Contains(rec.Body.String(), "GITHUB_TOKEN is now set") {
		t.Fatalf("evidence missing from goal detail: %s", rec.Body.String())
	}

	// Approving without a value stays acknowledge-only (approved is terminal).
	bare := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"OTHER_TOKEN"},"reason":"x","agent_name":"atlas"}`)
	acked, _ := decide(bare.ID, `{"note":"set it myself"}`)
	if acked.Status != store.AccessApproved {
		t.Fatalf("bare approval = %+v, want approved", acked)
	}

	// An invalid name fails the grant and stays retryable.
	bad := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"PATH"},"reason":"x","agent_name":"atlas"}`)
	failed, _ := decide(bad.ID, `{"secret_value":"v"}`)
	if failed.Status != store.AccessFailed || failed.ExecutionError == "" {
		t.Fatalf("reserved-name grant = %+v, want failed", failed)
	}
}

// refreshCounterAdapter is a Fake that also records RefreshCredentials calls,
// so the grant path's credential-propagation call can be asserted.
type refreshCounterAdapter struct {
	*adapter.Fake
	calls atomic.Int64
}

func (r *refreshCounterAdapter) RefreshCredentials() { r.calls.Add(1) }

func TestEnvVarGrantRefreshesCredentials(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer db.Close()
	spy := &refreshCounterAdapter{Fake: adapter.NewFake()}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: spy, Credentials: creds.New(paths.CredentialsYAML)})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Paths: paths})
	if _, err := coreSvc.CreateAgent(context.Background(), core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal := createGoalViaCore(t, srv, store.Goal{})

	file := func(body string) store.AccessRequest {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleAccessRequests(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("file request: %d %s", rr.Code, rr.Body.String())
		}
		var out store.AccessRequest
		if err := json.NewDecoder(rr.Body).Decode(&out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return out
	}
	approve := func(id, body string) store.AccessRequest {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/access-requests/"+id+"/approve", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		srv.handleAccessRequest(rr, req)
		var out store.AccessRequest
		_ = json.NewDecoder(rr.Body).Decode(&out)
		return out
	}

	// Approving with a secret stores the credential and must refresh so a
	// running provider process picks it up.
	withValue := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"GITHUB_TOKEN","purpose":"gh"},"reason":"repo","agent_name":"atlas"}`)
	if granted := approve(withValue.ID, `{"secret_value":"tok_s3cret"}`); granted.Status != store.AccessExecuted {
		t.Fatalf("grant = %+v, want executed", granted)
	}
	if got := spy.calls.Load(); got != 1 {
		t.Fatalf("RefreshCredentials called %d times after credential grant, want 1", got)
	}

	// Approving without a secret is acknowledge-only: nothing stored, no refresh.
	bare := file(`{"goal_id":"` + goal.ID + `","kind":"env_var","payload":{"var_name":"OTHER_TOKEN"},"reason":"x","agent_name":"atlas"}`)
	if acked := approve(bare.ID, `{"note":"set it myself"}`); acked.Status != store.AccessApproved {
		t.Fatalf("bare approval = %+v, want approved", acked)
	}
	if got := spy.calls.Load(); got != 1 {
		t.Fatalf("RefreshCredentials called %d times; a value-free grant must not refresh", got)
	}
}

func TestGoalDetailAndDelete(t *testing.T) {
	_, srv, cleanup := newGoalTestServer(t)
	defer cleanup()
	goal := createGoalViaCore(t, srv, store.Goal{})

	req := httptest.NewRequest(http.MethodGet, "/api/goals/"+goal.ID, nil)
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rr.Code, rr.Body.String())
	}
	var detail GoalDetail
	if err := json.NewDecoder(rr.Body).Decode(&detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Goal.ID != goal.ID || len(detail.Events) != 1 || detail.Events[0].Kind != store.GoalEventCreated {
		t.Fatalf("detail = %+v", detail)
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/goals/"+goal.ID, nil)
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := srv.core.GetGoal(context.Background(), goal.ID); err == nil {
		t.Fatalf("goal should be gone")
	}
}

// TestCLIToolStaysAcknowledgeOnly guards the rule that a cli_tool approval
// never installs anything. Agents provision their own tools through the shared
// toolset, so a cli_tool request means only "the user must install this
// host-wide" — including when it carries the installer fields the old
// per-agent grant used to act on, which are now inert.
func TestCLIToolStaysAcknowledgeOnly(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"host-only", `{"tool":"lychee","install_hint":"brew install lychee"}`},
		{"legacy installer fields", `{"tool":"lychee","installer":"npm","package":"lychee"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, srv, cleanup := newGoalTestServer(t)
			defer cleanup()
			goal := createGoalViaCore(t, srv, store.Goal{})

			body := `{"goal_id":"` + goal.ID + `","kind":"cli_tool","agent_name":"atlas","reason":"needs a tool",` +
				`"payload":` + tc.payload + `}`
			req := httptest.NewRequest(http.MethodPost, "/api/access-requests", bytes.NewBufferString(body))
			rr := httptest.NewRecorder()
			srv.handleAccessRequests(rr, req)
			var filed store.AccessRequest
			_ = json.NewDecoder(rr.Body).Decode(&filed)

			req = httptest.NewRequest(http.MethodPost, "/api/access-requests/"+filed.ID+"/approve", bytes.NewBufferString(`{"note":"done"}`))
			rr = httptest.NewRecorder()
			srv.handleAccessRequest(rr, req)
			var decided store.AccessRequest
			_ = json.NewDecoder(rr.Body).Decode(&decided)
			if decided.Status != store.AccessApproved {
				t.Fatalf("decided = %+v, want approved terminal", decided)
			}
			time.Sleep(100 * time.Millisecond)
			if got, _ := srv.core.GetAccessRequest(context.Background(), filed.ID); got.Status != store.AccessApproved {
				t.Fatalf("request moved to %q; must stay approved", got.Status)
			}
			if list, _ := podiomtools.List(srv.paths.ToolsetDir); len(list) != 0 {
				t.Fatalf("a cli_tool approval must not install anything: %+v", list)
			}
		})
	}
}

// newGoalSchedulerTestServer wires a real Scheduler alongside the goal API so
// the goal→schedule cascade can be exercised end to end.
func newGoalSchedulerTestServer(t *testing.T) (config.Paths, *Server, *schedule.Scheduler, func()) {
	t.Helper()
	home := t.TempDir()
	paths := config.NewPaths(home)
	if _, err := config.Scaffold(paths); err != nil {
		t.Fatalf("scaffold: %v", err)
	}
	db, err := store.Open(paths.DB)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	coreSvc, err := core.New(core.Options{Paths: paths, Store: db, Adapter: adapter.NewFake()})
	if err != nil {
		t.Fatalf("new core: %v", err)
	}
	sched := schedule.New(schedule.Options{Dir: paths.SchedulesDir, Core: coreSvc, Store: db})
	srv := New(Options{Bind: "127.0.0.1", Port: 0, Core: coreSvc, Scheduler: sched, Paths: paths})
	if _, err := coreSvc.CreateAgent(context.Background(), core.CreateAgentRequest{Name: "atlas", Provider: config.ProviderClaude}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	return paths, srv, sched, func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close store: %v", err)
		}
	}
}

func goalLinkedSchedule(t *testing.T, sched *schedule.Scheduler, name, goalID string) {
	t.Helper()
	if _, err := sched.Create(context.Background(), schedule.CreateParams{
		Name:   name,
		Agent:  "atlas",
		Cron:   "0 0 * * *",
		GoalID: goalID,
		Body:   "do the goal's recurring work",
	}); err != nil {
		t.Fatalf("create schedule %q: %v", name, err)
	}
}

func schedulesForGoal(t *testing.T, sched *schedule.Scheduler, goalID string) []string {
	t.Helper()
	statuses, err := sched.List(context.Background())
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	var names []string
	for _, st := range statuses {
		if st.GoalID == goalID {
			names = append(names, st.Name)
		}
	}
	return names
}

// TestGoalTerminalDeletesLinkedSchedules verifies that abandoning a goal (and
// deleting another) tears down only that goal's schedules, leaving unrelated
// ones untouched.
func TestGoalTerminalDeletesLinkedSchedules(t *testing.T) {
	paths, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()

	goalA := createGoalViaCore(t, srv, store.Goal{Title: "Goal A"})
	goalB := createGoalViaCore(t, srv, store.Goal{Title: "Goal B"})
	goalLinkedSchedule(t, sched, "a-daily", goalA.ID)
	goalLinkedSchedule(t, sched, "b-daily", goalB.ID)

	if got := schedulesForGoal(t, sched, goalA.ID); len(got) != 1 {
		t.Fatalf("goal A schedules before abandon = %v, want 1", got)
	}

	// Abandon goal A — its schedule must go, goal B's must stay.
	req := httptest.NewRequest(http.MethodPatch, "/api/goals/"+goalA.ID, bytes.NewBufferString(`{"status":"abandoned"}`))
	rr := httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("abandon: %d %s", rr.Code, rr.Body.String())
	}
	if got := schedulesForGoal(t, sched, goalA.ID); len(got) != 0 {
		t.Fatalf("goal A schedules after abandon = %v, want none", got)
	}
	if got := schedulesForGoal(t, sched, goalB.ID); len(got) != 1 {
		t.Fatalf("goal B schedules after A abandoned = %v, want 1 (untouched)", got)
	}

	// Deleting goal B must remove its schedule too.
	req = httptest.NewRequest(http.MethodDelete, "/api/goals/"+goalB.ID, nil)
	rr = httptest.NewRecorder()
	srv.handleGoal(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if got := schedulesForGoal(t, sched, goalB.ID); len(got) != 0 {
		t.Fatalf("goal B schedules after delete = %v, want none", got)
	}
	if _, err := os.Stat(filepath.Join(paths.SchedulesDir, "b-daily.md")); !os.IsNotExist(err) {
		t.Fatalf("goal B schedule file should be gone, stat err = %v", err)
	}
}
