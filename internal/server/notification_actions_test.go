package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// postAction performs a notification action through the HTTP surface.
func postAction(t *testing.T, srv *Server, notificationID, actionID string) *httptest.ResponseRecorder {
	t.Helper()
	rr := httptest.NewRecorder()
	path := "/api/notifications/" + notificationID + "/actions/" + actionID
	srv.handleNotification(rr, httptest.NewRequest(http.MethodPost, path, nil))
	return rr
}

// TestStaleAccessRequestApprovalIsRejected is the scenario the design has to get
// right: a request notifies the phone, the user denies it at their desk, and later
// taps Approve on the notification that is still sitting on the lock screen.
//
// The approval must not overwrite the decision already made.
func TestStaleAccessRequestApprovalIsRejected(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, request := seedAccessRequest(t, srv, store.AccessMCPServer)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalAccessRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceAccessRequest), ResourceID: request.ID, Actionable: true,
	})

	// The desktop decides first.
	if _, err := srv.core.DecideAccessRequest(ctx, request.ID, false, "not now"); err != nil {
		t.Fatalf("deny from the dashboard: %v", err)
	}

	rr := postAction(t, srv, n.ID, string(notify.ActionApprove))
	if rr.Code != http.StatusConflict {
		t.Fatalf("stale approve status = %d, want 409; body=%s", rr.Code, rr.Body.String())
	}
	var stale notificationStaleResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &stale); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if stale.Status != "stale" {
		t.Errorf("Status = %q, want stale", stale.Status)
	}
	// The client is told what the resource looks like now, so it can explain the
	// conflict rather than only reporting one.
	if stale.Resource.State != string(store.AccessDenied) {
		t.Errorf("Resource.State = %q, want %q", stale.Resource.State, store.AccessDenied)
	}
	// And which actions it should be offering instead.
	for _, action := range stale.Actions {
		if action.ID == notify.ActionApprove {
			t.Error("stale response still offers approve")
		}
	}

	// The decision itself is untouched. This is the assertion that matters.
	after, err := db.GetAccessRequest(ctx, request.ID)
	if err != nil {
		t.Fatalf("get access request: %v", err)
	}
	if after.Status != store.AccessDenied {
		t.Errorf("access request status = %q, want %q — a stale notification action "+
			"overwrote a decision the user had already made", after.Status, store.AccessDenied)
	}
}

// TestApproveAccessRequestFromNotification checks the happy path performs the real
// domain operation and settles the notification.
func TestApproveAccessRequestFromNotification(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, request := seedAccessRequest(t, srv, store.AccessMCPServer)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalAccessRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceAccessRequest), ResourceID: request.ID, Actionable: true,
	})

	rr := postAction(t, srv, n.ID, string(notify.ActionApprove))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	after, err := db.GetAccessRequest(ctx, request.ID)
	if err != nil {
		t.Fatalf("get access request: %v", err)
	}
	if after.Status == store.AccessPending {
		t.Error("the access request is still pending after approval")
	}
	// Acting from a notification must not leave that notification looking open.
	fresh, err := db.GetNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if fresh.ResolvedAt == "" {
		t.Error("the notification was not resolved after its action succeeded")
	}
}

// TestCredentialAccessRequestOffersNoDirectApproval checks a request that needs a
// secret value cannot be approved from a notification. Approving one means supplying
// the credential, which is not something to accept from a lock-screen button.
func TestCredentialAccessRequestOffersNoDirectApproval(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, request := seedAccessRequest(t, srv, store.AccessEnvVar)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalAccessRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceAccessRequest), ResourceID: request.ID, Actionable: true,
	})

	view := srv.notificationView(httptest.NewRequest(http.MethodGet, "/api/notifications", nil), n)
	if len(view.Actions) != 1 || view.Actions[0].ID != notify.ActionOpen {
		t.Errorf("credential request offers %+v, want navigation only", view.Actions)
	}
	// And the endpoint refuses even if a client asks anyway.
	if rr := postAction(t, srv, n.ID, string(notify.ActionApprove)); rr.Code != http.StatusConflict {
		t.Errorf("approve status = %d, want 409", rr.Code)
	}
	after, err := db.GetAccessRequest(ctx, request.ID)
	if err != nil {
		t.Fatalf("get access request: %v", err)
	}
	if after.Status != store.AccessPending {
		t.Errorf("status = %q, want still pending", after.Status)
	}
}

// TestGoalActionItemActionsFromNotification covers done and its stale case.
func TestGoalActionItemActionsFromNotification(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, item := seedGoalActionItem(t, srv)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalActionRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceGoalActionItem), ResourceID: item.ID, Actionable: true,
	})

	if rr := postAction(t, srv, n.ID, string(notify.ActionDone)); rr.Code != http.StatusOK {
		t.Fatalf("done status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	after, err := db.GetGoalActionItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("get action item: %v", err)
	}
	if after.Status != store.GoalActionDone {
		t.Errorf("status = %q, want %q", after.Status, store.GoalActionDone)
	}

	// A second verdict from another device must not overwrite the first.
	rr := postAction(t, srv, n.ID, string(notify.ActionBlocked))
	if rr.Code != http.StatusConflict {
		t.Errorf("second verdict status = %d, want 409", rr.Code)
	}
	again, err := db.GetGoalActionItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("get action item: %v", err)
	}
	if again.Status != store.GoalActionDone {
		t.Errorf("status = %q, want the original %q", again.Status, store.GoalActionDone)
	}
}

// TestOpenActionPerformsNoDomainWrite checks navigation is only navigation: tapping
// through to look at something must not decide it, and must not mark the ask
// settled.
func TestOpenActionPerformsNoDomainWrite(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, item := seedGoalActionItem(t, srv)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalActionRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceGoalActionItem), ResourceID: item.ID,
		NavTarget: notify.NavGoalActionItem, Actionable: true,
	})

	rr := postAction(t, srv, n.ID, string(notify.ActionOpen))
	if rr.Code != http.StatusOK {
		t.Fatalf("open status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var got notificationActionResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.NavTarget != notify.NavGoalActionItem {
		t.Errorf("NavTarget = %q, want %q", got.NavTarget, notify.NavGoalActionItem)
	}
	after, err := db.GetGoalActionItem(ctx, item.ID)
	if err != nil {
		t.Fatalf("get action item: %v", err)
	}
	if after.Status != store.GoalActionOpen {
		t.Errorf("action item status = %q, want still open — opening is not answering", after.Status)
	}
	fresh, err := db.GetNotification(ctx, n.ID)
	if err != nil {
		t.Fatalf("get notification: %v", err)
	}
	if fresh.ResolvedAt != "" {
		t.Error("opening a notification resolved it; the user has been sent to the resource, not finished with it")
	}
}

// TestAnswerQuestionByOptionIndex checks a predefined answer is selected by position
// and resolved to its label server-side, so nothing a client sends is treated as an
// instruction.
func TestAnswerQuestionByOptionIndex(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	ctx := context.Background()
	goal, question := seedAgentQuestion(t, srv, []store.AgentQuestionOption{
		{Label: "Stable"}, {Label: "Beta"},
	}, false, false)

	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalQuestion, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceAgentQuestion), ResourceID: question.ID, Actionable: true,
	})

	view := srv.notificationView(httptest.NewRequest(http.MethodGet, "/api/notifications", nil), n)
	labels := map[string]string{}
	for _, action := range view.Actions {
		labels[string(action.ID)] = action.Label
	}
	if labels["answer:1"] != "Beta" {
		t.Fatalf("answer:1 label = %q, want Beta; actions=%+v", labels["answer:1"], view.Actions)
	}

	if rr := postAction(t, srv, n.ID, "answer:1"); rr.Code != http.StatusOK {
		t.Fatalf("answer status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	answered, err := db.GetAgentQuestion(ctx, question.ID)
	if err != nil {
		t.Fatalf("get question: %v", err)
	}
	if answered.Status != store.AgentQuestionAnswered {
		t.Fatalf("status = %q, want answered", answered.Status)
	}
	got := answered.Answers[question.Questions[0].ID]
	if len(got) != 1 || got[0] != "Beta" {
		t.Errorf("answer = %v, want [Beta]", got)
	}

	// Answering twice must not overwrite the recorded answer.
	if rr := postAction(t, srv, n.ID, "answer:0"); rr.Code != http.StatusConflict {
		t.Errorf("second answer status = %d, want 409", rr.Code)
	}
}

// TestSecretAndMultiSelectQuestionsOfferNoAnswers checks the shapes a notification
// cannot honestly represent open the app instead. A secret in particular must never
// be answerable from a lock screen.
func TestSecretAndMultiSelectQuestionsOfferNoAnswers(t *testing.T) {
	tests := []struct {
		name        string
		secret      bool
		multiSelect bool
	}{
		{"secret", true, false},
		{"multi-select", false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, db, _ := newNotifyTestServer(t)
			goal, question := seedAgentQuestion(t, srv, []store.AgentQuestionOption{
				{Label: "Stable"}, {Label: "Beta"},
			}, tc.secret, tc.multiSelect)

			n := seedNotification(t, db, store.Notification{
				Type: notify.TypeGoalQuestion, GoalID: goal.ID,
				ResourceKind: string(notify.ResourceAgentQuestion), ResourceID: question.ID, Actionable: true,
			})
			view := srv.notificationView(httptest.NewRequest(http.MethodGet, "/api/notifications", nil), n)
			if len(view.Actions) != 1 || view.Actions[0].ID != notify.ActionOpen {
				t.Errorf("actions = %+v, want navigation only", view.Actions)
			}
			if rr := postAction(t, srv, n.ID, "answer:0"); rr.Code != http.StatusConflict {
				t.Errorf("answer status = %d, want 409", rr.Code)
			}
		})
	}
}

// TestNotificationActionRejectsUnknownAction checks only the closed set of action
// identifiers is accepted.
func TestNotificationActionRejectsUnknownAction(t *testing.T) {
	srv, db, _ := newNotifyTestServer(t)
	goal, item := seedGoalActionItem(t, srv)
	n := seedNotification(t, db, store.Notification{
		Type: notify.TypeGoalActionRequested, GoalID: goal.ID,
		ResourceKind: string(notify.ResourceGoalActionItem), ResourceID: item.ID, Actionable: true,
	})

	for _, actionID := range []string{"delete-everything", "answer:99", "answer:x", "ALLOW"} {
		t.Run(actionID, func(t *testing.T) {
			if rr := postAction(t, srv, n.ID, actionID); rr.Code != http.StatusConflict {
				t.Errorf("status = %d, want 409", rr.Code)
			}
		})
	}
}

// seedAccessRequest files a pending access request of the given kind.
func seedAccessRequest(t *testing.T, srv *Server, kind store.AccessRequestKind) (store.Goal, store.AccessRequest) {
	t.Helper()
	ctx := context.Background()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{
		Name: "lead", Provider: config.ProviderClaude,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := srv.core.CreateGoal(ctx, store.Goal{Title: "Launch Podiom", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// Each kind validates its own payload shape; env_var names the variable and
	// deliberately never carries a value.
	payload := map[string]string{"server_name": "github-mcp"}
	if kind == store.AccessEnvVar {
		payload = map[string]string{"var_name": "GITHUB_TOKEN"}
	}
	request, err := srv.core.CreateAccessRequest(ctx, core.CreateAccessRequestInput{
		GoalID: goal.ID, AgentName: "lead", Kind: kind, Payload: payload,
		Reason: "needed to publish the release",
	})
	if err != nil {
		t.Fatalf("create access request: %v", err)
	}
	return goal, request
}

// seedAgentQuestion files a pending deferred question on a goal-origin session.
func seedAgentQuestion(t *testing.T, srv *Server, options []store.AgentQuestionOption, secret, multiSelect bool) (store.Goal, store.AgentQuestion) {
	t.Helper()
	ctx := context.Background()
	if _, err := srv.core.CreateAgent(ctx, core.CreateAgentRequest{
		Name: "lead", Provider: config.ProviderClaude,
	}); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	goal, err := srv.core.CreateGoal(ctx, store.Goal{Title: "Launch Podiom", LeadAgent: "lead"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sess, err := srv.core.CreateSession(ctx, core.CreateSessionRequest{
		AgentName: "lead", Origin: store.OriginGoal, GoalID: goal.ID,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	res, err := srv.core.CreateAgentQuestion(ctx, sess.ID, []store.AgentQuestionItem{{
		Question: "Which release channel should I use?",
		Options:  options, IsSecret: secret, MultiSelect: multiSelect,
	}})
	if err != nil {
		t.Fatalf("create question: %v", err)
	}
	return goal, res.Question
}
