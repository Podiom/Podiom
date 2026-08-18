package notify

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Podiom/Podiom/internal/store"
)

// maxNativeChoices caps how many predefined answers a notification offers as
// actions. Native notification surfaces collapse past two or three buttons, so a
// longer list is better answered in the app than guessed at from a lock screen.
const maxNativeChoices = 3

// LiveActions returns the operations a notification can offer right now.
//
// Actions are derived per read rather than stored on the row, because what a
// notification can do depends on domain state that keeps moving after it was
// recorded: an access request approved from the dashboard must stop offering
// Approve on a phone still displaying the notification. Only the fact that a
// notification is actionable at all is persisted, as a rendering hint.
//
// A resolved, decided, or otherwise unavailable object degrades to navigation.
func (e *Engine) LiveActions(ctx context.Context, n store.Notification) []Action {
	if e == nil {
		return nil
	}
	info, ok := Lookup(n.Type)
	if !ok || len(info.Actions) == 0 {
		return nil
	}
	// A notification the user has already dealt with offers a way back to the
	// resource and nothing more.
	if n.ResolvedAt != "" {
		return navigationOnly(info)
	}

	switch ResourceKind(n.ResourceKind) {
	case ResourceGoalActionItem:
		item, err := e.store.GetGoalActionItem(ctx, n.ResourceID)
		if err != nil || item.Status != store.GoalActionOpen {
			return navigationOnly(info)
		}
		return labelled(info.Actions)

	case ResourceAccessRequest:
		req, err := e.store.GetAccessRequest(ctx, n.ResourceID)
		if err != nil {
			return navigationOnly(info)
		}
		// A failed grant is still actionable — it can be retried — but a decided
		// one is not.
		if req.Status != store.AccessPending && req.Status != store.AccessFailed {
			return navigationOnly(info)
		}
		// Approving a credential request means supplying the secret value, which
		// has no place in a notification action.
		if req.Kind == store.AccessEnvVar {
			return navigationOnly(info)
		}
		return labelled(info.Actions)

	case ResourceAgentQuestion:
		q, err := e.store.GetAgentQuestion(ctx, n.ResourceID)
		if err != nil || q.Status != store.AgentQuestionPending {
			return navigationOnly(info)
		}
		return questionActions(info, q.Questions)

	case ResourceSessionQuestion, ResourcePermissionRequest:
		// These live in memory, not the database, so their state comes from the
		// broker predicate rather than a row.
		if !e.pending(ResourceKind(n.ResourceKind), n.ResourceID) {
			return navigationOnly(info)
		}
		return labelled(info.Actions)

	case ResourceGoalCompletion:
		goal, err := e.store.GetGoal(ctx, n.ResourceID)
		if err != nil || goal.Status != store.GoalReview {
			return navigationOnly(info)
		}
		return labelled(info.Actions)
	}

	return labelled(info.Actions)
}

// questionActions offers a question's predefined options as actions, but only for
// the shape a notification can honestly represent: one question, a short list of
// preset choices, and no free-form, secret, or multi-select answer.
//
// Anything else opens the app. A secret in particular must never be typed into a
// lock-screen action.
func questionActions(info Info, items []store.AgentQuestionItem) []Action {
	if len(items) != 1 {
		return navigationOnly(info)
	}
	item := items[0]
	if item.IsSecret || item.MultiSelect || item.IsOther {
		return navigationOnly(info)
	}
	if len(item.Options) == 0 || len(item.Options) > maxNativeChoices {
		return navigationOnly(info)
	}
	actions := navigationOnly(info)
	for i, opt := range item.Options {
		// The action id carries the option's position, never its text: the client
		// sends back a choice, not a string for the server to act on.
		actions = append(actions, Action{
			ID:    ActionID(fmt.Sprintf("%s%d", ActionAnswerPrefix, i)),
			Label: opt.Label,
		})
	}
	return actions
}

// navigationOnly reduces a type's action set to its way in.
func navigationOnly(info Info) []Action {
	for _, id := range info.Actions {
		if id == ActionOpen || id == ActionReview {
			return []Action{{ID: id, Label: actionLabels[id]}}
		}
	}
	return nil
}

// labelled renders a candidate action set with its display text.
func labelled(ids []ActionID) []Action {
	out := make([]Action, 0, len(ids))
	for _, id := range ids {
		out = append(out, Action{ID: id, Label: actionLabels[id]})
	}
	return out
}

// actionLabels is the display text for each action id. Kept beside the ids so a
// new action cannot ship without wording.
var actionLabels = map[ActionID]string{
	ActionOpen:     "Open",
	ActionAllow:    "Allow",
	ActionDeny:     "Deny",
	ActionApprove:  "Approve",
	ActionDone:     "Done",
	ActionBlocked:  "Can't do",
	ActionReview:   "Review",
	ActionMarkDone: "Mark done",
}

// goalEventResourceID pulls the actionable object's id out of a goal event's
// payload. Each resolving and requesting event kind already records the id under
// its own key, so this is the bridge between the timeline and the notification's
// resource reference.
func goalEventResourceID(ev store.GoalEvent) string {
	if ev.Payload == "" {
		return ""
	}
	var payload struct {
		RequestID    string `json:"request_id"`
		QuestionID   string `json:"question_id"`
		ActionItemID string `json:"action_item_id"`
		BlockID      string `json:"block_id"`
	}
	if err := json.Unmarshal([]byte(ev.Payload), &payload); err != nil {
		return ""
	}
	return firstNonEmpty(payload.RequestID, payload.QuestionID, payload.ActionItemID, payload.BlockID)
}
