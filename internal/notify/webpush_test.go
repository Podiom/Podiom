package notify

import (
	"encoding/json"
	"testing"
)

func TestWebPushPayloadIncludesApprovalAction(t *testing.T) {
	payload := webPushPayloadForEnvelope(Envelope{
		SessionID: "session-1",
		Title:     "Agent needs approval",
		Body:      "A tool action is waiting for your decision.",
		PushKind:  legacyKindPermission,
		Approval: &ApprovalAction{
			RequestID: "perm-1",
			Input:     json.RawMessage(`{"command":"echo ok"}`),
		},
	})

	if payload.Approval == nil {
		t.Fatal("approval action was not included")
	}
	if payload.Approval.RequestID != "perm-1" {
		t.Fatalf("request id = %q, want perm-1", payload.Approval.RequestID)
	}
	if string(payload.Approval.Input) != `{"command":"echo ok"}` {
		t.Fatalf("approval input = %s", payload.Approval.Input)
	}
}

func TestWebPushPayloadOmitsApprovalActionForQuestions(t *testing.T) {
	payload := webPushPayloadForEnvelope(Envelope{
		SessionID: "session-1",
		Title:     "Agent has a question",
		PushKind:  legacyKindQuestion,
	})

	if payload.Approval != nil {
		t.Fatalf("question payload should not include approval action: %+v", payload.Approval)
	}
}

// TestWebPushPayloadStaysBackwardCompatible pins the payload shape the shipped
// service worker reads. web/public/sw.js keys its behaviour off these exact field
// names and off kind == "permission" for the Approve button, so a rename here
// would silently break notifications for every browser already subscribed.
func TestWebPushPayloadStaysBackwardCompatible(t *testing.T) {
	raw, err := json.Marshal(webPushPayloadForEnvelope(Envelope{
		ID:         "not-1",
		Title:      "Alice needs approval",
		Body:       "A tool action is waiting for your decision.",
		SessionID:  "session-1",
		GoalID:     "goal-1",
		PushKind:   legacyKindPermission,
		NavTarget:  NavSessionPermission,
		ResourceID: "perm-1",
		Approval:   &ApprovalAction{RequestID: "perm-1", Input: json.RawMessage(`{"command":"ls"}`)},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, field := range []string{"title", "body", "session_id", "goal_id", "kind", "approval"} {
		if _, ok := got[field]; !ok {
			t.Errorf("payload is missing %q, which sw.js reads", field)
		}
	}
	if got["kind"] != legacyKindPermission {
		t.Errorf("kind = %v, want %q — sw.js gates its Approve action on this value",
			got["kind"], legacyKindPermission)
	}
	// The routing fields added with the engine let a browser tap land on the exact
	// resource. They are additive: the assertions above cover the original shape that
	// the shipped service worker depends on.
	for _, field := range []string{"notification_id", "nav_target", "resource_id"} {
		if _, ok := got[field]; !ok {
			t.Errorf("payload is missing the routing field %q", field)
		}
	}

	approval, ok := got["approval"].(map[string]any)
	if !ok {
		t.Fatalf("approval is %T, want an object", got["approval"])
	}
	for _, field := range []string{"request_id", "input"} {
		if _, ok := approval[field]; !ok {
			t.Errorf("approval is missing %q, which sw.js reads", field)
		}
	}
}

// TestLegacyPushKindsCoverPreEngineTypes checks every notification type that
// existed before the engine still reports its original push kind, so the service
// worker's existing behaviour keeps applying to it.
func TestLegacyPushKindsCoverPreEngineTypes(t *testing.T) {
	tests := []struct {
		notifType string
		want      string
	}{
		{TypeSessionPermissionRequired, legacyKindPermission},
		{TypeSessionQuestion, legacyKindQuestion},
		{TypeGoalQuestion, legacyKindQuestion},
		{TypeScheduleQuestion, legacyKindQuestion},
		{TypeGoalAccessRequested, legacyKindGoalAccess},
		{TypeGoalCompletionProposed, legacyKindGoalReview},
		{TypeGoalRateLimited, legacyKindGoalRateLimit},
		{TypeGoalActionRequested, legacyKindGoalActionItem},
		// A type introduced with the engine has no legacy kind and falls back to
		// its own identifier.
		{TypeGoalProgress, TypeGoalProgress},
	}
	for _, tc := range tests {
		t.Run(tc.notifType, func(t *testing.T) {
			info, ok := Lookup(tc.notifType)
			if !ok {
				t.Fatalf("%q is not registered", tc.notifType)
			}
			if got := info.PushKind(); got != tc.want {
				t.Errorf("PushKind() = %q, want %q", got, tc.want)
			}
		})
	}
}
