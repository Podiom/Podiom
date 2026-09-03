package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// notificationActionResponse is returned when an action succeeds. NavTarget lets a
// client that tapped "open" route without a second request.
type notificationActionResponse struct {
	Status       string           `json:"status"`
	Notification notificationView `json:"notification"`
	NavTarget    string           `json:"nav_target"`
}

// notificationStaleResponse is returned with 409 when the requested action is no
// longer valid.
//
// It carries the actions that *are* valid plus the resource's current state, so a
// client can correct the notification it is showing instead of just reporting an
// error — and so it can tell "already done, as I intended" apart from "someone
// else resolved this differently".
type notificationStaleResponse struct {
	Status       string               `json:"status"`
	Reason       string               `json:"reason"`
	Actions      []notify.Action      `json:"actions"`
	Resource     notificationResource `json:"resource"`
	Notification notificationView     `json:"notification"`
}

type notificationResource struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	State string `json:"state"`
}

// handleNotificationAction performs a notification's action at
// /api/notifications/{id}/actions/{actionID}.
//
// This exists so a client's whole vocabulary is (notification id, action id). The
// alternative — having the mobile app call the five underlying domain endpoints
// directly — would mean holding a map of resource kind to endpoint to body shape in
// TypeScript and Swift and Kotlin, duplicating the server's registry in three
// places that would drift apart.
//
// It reimplements nothing: every branch calls the same core method the web UI's own
// handler calls, so an action from a notification and a click in the dashboard are
// the same operation. Keeping it that way matters — this is a second door to those
// operations, so any future authorization rule has to live in core rather than in a
// handler, or the doors will disagree.
func (s *Server) handleNotificationAction(w http.ResponseWriter, r *http.Request, id, actionID string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if actionID == "" {
		http.Error(w, "action id is required", http.StatusBadRequest)
		return
	}
	db := s.core.Store()
	row, err := db.GetNotification(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}

	// Recomputed against live domain state, not read from the stored row: this is
	// the first of three defences against a stale action, and the one that lets the
	// client be told what it should be showing instead.
	live := s.notifications.LiveActions(r.Context(), row)
	if !actionOffered(live, actionID) {
		s.writeStale(w, r, row, "action is no longer available")
		return
	}

	nav, err := s.performNotificationAction(r, row, notify.ActionID(actionID))
	if err != nil {
		// The guarded updates underneath are the second defence, and the one that
		// settles a genuine race between two devices acting at once.
		if isStaleActionError(err) {
			s.writeStale(w, r, row, err.Error())
			return
		}
		writeJSON(w, nil, err)
		return
	}

	// Navigation is not a decision, so it neither resolves the notification nor
	// marks it read — the user has been sent to the resource, not finished with it.
	if nav != "" {
		writeJSON(w, notificationActionResponse{
			Status: "ok", Notification: s.notificationView(r, row), NavTarget: nav,
		}, nil)
		return
	}

	// The operation succeeded, so the ask is settled. Resolving here means an action
	// taken from a phone can never leave the notification looking open.
	resolved, rerr := db.ResolveNotification(r.Context(), row.ID)
	if rerr != nil {
		// Already resolved by the domain hook that the operation itself triggered;
		// re-read so the response reflects the truth.
		if fresh, ferr := db.GetNotification(r.Context(), row.ID); ferr == nil {
			resolved = fresh
		} else {
			resolved = row
		}
	}
	s.broadcastNotificationUpdate(resolved)
	writeJSON(w, notificationActionResponse{
		Status: "ok", Notification: s.notificationView(r, resolved), NavTarget: resolved.NavTarget,
	}, nil)
}

// performNotificationAction maps a stable action id onto the domain operation it
// means. A non-empty return value means the action was navigation only.
func (s *Server) performNotificationAction(r *http.Request, row store.Notification, actionID notify.ActionID) (string, error) {
	ctx := r.Context()
	kind := notify.ResourceKind(row.ResourceKind)

	// Navigation performs no domain write at all, whatever the resource.
	if actionID == notify.ActionOpen || actionID == notify.ActionReview {
		return orFallback(row.NavTarget, notify.NavSession), nil
	}

	switch {
	case kind == notify.ResourceGoalActionItem && actionID == notify.ActionDone:
		_, err := s.core.RespondGoalActionItem(ctx, row.ResourceID, store.GoalActionDone, "")
		return "", err
	case kind == notify.ResourceGoalActionItem && actionID == notify.ActionBlocked:
		_, err := s.core.RespondGoalActionItem(ctx, row.ResourceID, store.GoalActionBlocked, "")
		return "", err

	case kind == notify.ResourceAccessRequest && (actionID == notify.ActionApprove || actionID == notify.ActionDeny):
		decided, err := s.core.DecideAccessRequest(ctx, row.ResourceID, actionID == notify.ActionApprove, "")
		if err != nil {
			return "", err
		}
		if actionID == notify.ActionApprove {
			// No secret value is passed: a request needing one is never offered as a
			// notification action in the first place, because typing a credential into
			// a lock-screen button is not something to support.
			decided = s.executeAccessGrant(ctx, decided, "")
		}
		s.broadcastGoalPing(ctx, decided.GoalID)
		return "", nil

	case kind == notify.ResourcePermissionRequest && (actionID == notify.ActionAllow || actionID == notify.ActionDeny):
		behavior := "deny"
		if actionID == notify.ActionAllow {
			behavior = "allow"
		}
		decided := s.broker.decide(row.ResourceID, adapter.PermissionDecision{Behavior: behavior})
		restored := s.markRoadmapPermissionResolved(ctx, row.ResourceID)
		if !decided && !restored {
			return "", errStaleAction
		}
		return "", nil

	case kind == notify.ResourceGoalCompletion && actionID == notify.ActionMarkDone:
		// The goal state machine only permits done from review, so an already-settled
		// proposal is rejected by the domain itself rather than by a check here.
		goal, err := s.core.TransitionGoal(ctx, row.ResourceID, store.GoalDone, "")
		if err != nil {
			return "", err
		}
		s.broadcastGoalPing(ctx, goal.ID)
		return "", nil

	case kind == notify.ResourceAgentQuestion && strings.HasPrefix(string(actionID), notify.ActionAnswerPrefix):
		return "", s.answerQuestionByOptionIndex(r, row, string(actionID))
	}

	return "", errStaleAction
}

// answerQuestionByOptionIndex answers a deferred question by the position of the
// chosen option.
//
// The client sends an index, never text. That is what keeps the action vocabulary
// closed: the server resolves the index to the option's label itself, so nothing a
// client sends is ever treated as an instruction.
func (s *Server) answerQuestionByOptionIndex(r *http.Request, row store.Notification, actionID string) error {
	ctx := r.Context()
	index, err := strconv.Atoi(strings.TrimPrefix(actionID, notify.ActionAnswerPrefix))
	if err != nil || index < 0 {
		return errStaleAction
	}
	question, err := s.core.Store().GetAgentQuestion(ctx, row.ResourceID)
	if err != nil {
		return err
	}
	if len(question.Questions) != 1 {
		return errStaleAction
	}
	item := question.Questions[0]
	if index >= len(item.Options) {
		return errStaleAction
	}
	res, err := s.core.AnswerAgentQuestion(ctx, row.ResourceID, map[string][]string{
		item.ID: {item.Options[index].Label},
	})
	if err != nil {
		return err
	}
	if res.Event != nil {
		s.broadcastGoalEvent(*res.Event)
	}
	if res.Goal != nil {
		s.broadcastGoalPing(ctx, res.Goal.ID)
	}
	if res.Question.Origin == store.AgentQuestionSchedule {
		s.broadcastScheduleAttention(ctx)
	}
	return nil
}

// writeStale reports that an action cannot be performed any more, with 409 rather
// than 400 so a client can tell "refresh, this moved on" from "you sent nonsense".
func (s *Server) writeStale(w http.ResponseWriter, r *http.Request, row store.Notification, reason string) {
	fresh := row
	if updated, err := s.core.Store().GetNotification(r.Context(), row.ID); err == nil {
		fresh = updated
	}
	view := s.notificationView(r, fresh)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(notificationStaleResponse{
		Status:  "stale",
		Reason:  reason,
		Actions: view.Actions,
		Resource: notificationResource{
			Kind:  fresh.ResourceKind,
			ID:    fresh.ResourceID,
			State: s.resourceState(r, fresh),
		},
		Notification: view,
	})
}

// resourceState describes what the domain object looks like now, so a client can
// explain the conflict rather than only reporting one.
func (s *Server) resourceState(r *http.Request, row store.Notification) string {
	ctx := r.Context()
	db := s.core.Store()
	switch notify.ResourceKind(row.ResourceKind) {
	case notify.ResourceGoalActionItem:
		if item, err := db.GetGoalActionItem(ctx, row.ResourceID); err == nil {
			return string(item.Status)
		}
	case notify.ResourceAccessRequest:
		if req, err := db.GetAccessRequest(ctx, row.ResourceID); err == nil {
			return string(req.Status)
		}
	case notify.ResourceAgentQuestion:
		if question, err := db.GetAgentQuestion(ctx, row.ResourceID); err == nil {
			return string(question.Status)
		}
	case notify.ResourceGoalCompletion:
		if goal, err := s.core.GetGoal(ctx, row.ResourceID); err == nil {
			return string(goal.Status)
		}
	case notify.ResourcePermissionRequest:
		if s.broker.isPending(row.ResourceID) {
			return "pending"
		}
		return "decided"
	}
	if row.ResolvedAt != "" {
		return "resolved"
	}
	return "unknown"
}

// errStaleAction is the sentinel for an action the current domain state does not
// permit.
var errStaleAction = errors.New("the request has already been handled")

// isStaleActionError reports whether an error means "someone got here first".
//
// The guarded updates in the store already express this: responding to an action
// item, answering a question and deciding an access request all refuse to touch a
// row that has moved on. Mapping those to 409 happens only in this handler — doing
// it inside writeJSON would silently change the status codes of every existing
// endpoint.
func isStaleActionError(err error) bool {
	return errors.Is(err, errStaleAction) ||
		errors.Is(err, store.ErrAlreadyDecided) ||
		errors.Is(err, store.ErrNotFound)
}

// actionOffered reports whether an action id is in the currently valid set.
func actionOffered(actions []notify.Action, actionID string) bool {
	for _, action := range actions {
		if string(action.ID) == actionID {
			return true
		}
	}
	return false
}

func orFallback(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
