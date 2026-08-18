package server

import (
	"context"

	"github.com/Podiom/Podiom/internal/notify"
)

func (s *Server) markRoadmapQuestionPending(ctx context.Context, sessionID, requestID string) {
	if s.core == nil || requestID == "" || sessionID == "" {
		return
	}
	moved, err := s.core.MoveRoadmapSessionTaskForQuestion(ctx, sessionID)
	if err != nil {
		return
	}
	s.input.attach(requestID, sessionID, moved)
}

func (s *Server) markRoadmapQuestionResolved(ctx context.Context, requestID string) bool {
	if s.core == nil || requestID == "" {
		return false
	}
	meta := s.input.popMeta(requestID)
	if !meta.restoreRoadmap || meta.sessionID == "" {
		return false
	}
	_ = s.core.RestoreRoadmapSessionTaskAfterQuestion(ctx, meta.sessionID)
	return true
}

func (s *Server) markRoadmapPermissionPending(ctx context.Context, sessionID, requestID string) {
	if s.core == nil || requestID == "" || sessionID == "" {
		return
	}
	if sess, err := s.core.GetSession(ctx, sessionID); err == nil && sess.PlanState == "pending_submission" {
		s.broker.attach(requestID, sessionID, false)
		return
	}
	moved, err := s.core.MoveRoadmapSessionTaskToReview(ctx, sessionID)
	if err != nil {
		return
	}
	s.broker.attach(requestID, sessionID, moved)
}

func (s *Server) markRoadmapPermissionResolved(ctx context.Context, requestID string) bool {
	if s.core == nil || requestID == "" {
		return false
	}
	meta := s.broker.popMeta(requestID)
	if !meta.restoreRoadmap || meta.sessionID == "" {
		return false
	}
	_ = s.core.RestoreRoadmapSessionTaskToInProgress(ctx, meta.sessionID)
	return true
}

func (s *Server) markRoadmapSessionFinished(ctx context.Context, sessionID string) {
	if s.core == nil || sessionID == "" {
		return
	}
	sess, err := s.core.GetSession(ctx, sessionID)
	if err == nil && sess.PlanState == "pending_submission" {
		return
	}
	moved, merr := s.core.MoveRoadmapSessionTaskToReview(ctx, sessionID)
	if merr != nil || !moved || err != nil {
		return
	}
	// Only notify when this call is what moved the task, and only from here.
	//
	// The underlying transition is also used transiently — while a plan awaits
	// approval, and while a permission or question is pending — and notifying on
	// those would announce "ready for review" for work that is still running and
	// then immediately un-announce it. Reaching review because the agent's turn
	// finished is the one case that means what it says.
	s.notifications.Publish(notify.Event{
		Type:       notify.TypeTaskReviewRequired,
		SessionID:  sessionID,
		GoalID:     sess.GoalID,
		TaskID:     sess.TaskID,
		AgentName:  sess.AgentName,
		Resource:   notify.ResourceTask,
		ResourceID: sess.TaskID,
	})
}
