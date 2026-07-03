package server

import "context"

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
	if sess, err := s.core.GetSession(ctx, sessionID); err == nil && sess.PlanState == "pending_submission" {
		return
	}
	_, _ = s.core.MoveRoadmapSessionTaskToReview(ctx, sessionID)
}
