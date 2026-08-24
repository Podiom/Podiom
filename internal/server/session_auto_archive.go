package server

import "time"

const autoArchiveInterval = time.Hour

// kickAutoArchive asks the serialized runner for an immediate pass. The
// buffered signal keeps Settings writes non-blocking and coalesces rapid saves.
func (s *Server) kickAutoArchive() {
	if s == nil || s.autoArchiveKick == nil {
		return
	}
	select {
	case s.autoArchiveKick <- struct{}{}:
	default:
	}
}

func (s *Server) autoArchiveLoop() {
	ticker := time.NewTicker(autoArchiveInterval)
	defer ticker.Stop()

	s.runAutoArchive(time.Now())
	for {
		select {
		case <-s.autoArchiveCtx.Done():
			return
		case now := <-ticker.C:
			s.runAutoArchive(now)
		case <-s.autoArchiveKick:
			s.runAutoArchive(time.Now())
		}
	}
}

func (s *Server) runAutoArchive(now time.Time) {
	if s.core == nil {
		return
	}
	sessions, err := s.core.AutoArchiveInactiveSessions(s.autoArchiveCtx, now)
	for i := range sessions {
		session := sessions[i]
		s.broadcastWS(ServerMessage{Type: "session", SessionID: session.ID, Session: &session})
	}
	if err != nil {
		s.log.Warn("automatic session archive failed", "event", "run", "error", err)
		return
	}
	if len(sessions) > 0 {
		s.log.Info("inactive sessions archived", "event", "run", "count", len(sessions))
	}
}
