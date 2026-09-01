package server

import (
	"context"
	"sort"

	"github.com/Podiom/Podiom/internal/store"
)

// scheduleAttention returns the standalone schedules currently waiting for a
// user answer. Goal-linked schedule questions belong to the goal surface and use
// the existing goal-attention path instead.
func (s *Server) scheduleAttention(ctx context.Context) ([]string, error) {
	questions, err := s.core.Store().ListPendingAgentQuestions(ctx, store.AgentQuestionSchedule)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(questions))
	seen := make(map[string]struct{}, len(questions))
	for _, question := range questions {
		if question.RefID == "" {
			continue
		}
		if _, ok := seen[question.RefID]; ok {
			continue
		}
		seen[question.RefID] = struct{}{}
		names = append(names, question.RefID)
	}
	sort.Strings(names)
	return names, nil
}

// broadcastScheduleAttention refreshes every open dashboard after a standalone
// schedule question is created, answered, or removed.
func (s *Server) broadcastScheduleAttention(ctx context.Context) {
	if s.core == nil {
		return
	}
	names, err := s.scheduleAttention(ctx)
	if err != nil {
		s.log.Warn("read schedule attention failed", "event", "question", "err", err)
		return
	}
	s.broadcastWS(ServerMessage{Type: "schedule_attention", ScheduleAttention: names})
}
