package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateAgentQuestion inserts a pending question raised by an unattended run.
func (s *Store) CreateAgentQuestion(ctx context.Context, q AgentQuestion) (AgentQuestion, error) {
	if q.ID == "" {
		q.ID = uuid.NewString()
	}
	if q.Status == "" {
		q.Status = AgentQuestionPending
	}
	if q.Questions == nil {
		q.Questions = []AgentQuestionItem{}
	}
	questionsJSON, err := json.Marshal(q.Questions)
	if err != nil {
		return AgentQuestion{}, fmt.Errorf("marshal agent question items: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO agent_questions
		(id, origin, ref_id, session_id, questions_json, status)
		VALUES (?, ?, ?, ?, ?, ?)`,
		q.ID, q.Origin, q.RefID, q.SessionID, string(questionsJSON), q.Status); err != nil {
		return AgentQuestion{}, fmt.Errorf("create agent question for %s %q: %w", q.Origin, q.RefID, err)
	}
	return s.GetAgentQuestion(ctx, q.ID)
}

// GetAgentQuestion fetches one question by id.
func (s *Store) GetAgentQuestion(ctx context.Context, id string) (AgentQuestion, error) {
	row := s.db.QueryRowContext(ctx, agentQuestionSelect+` WHERE id = ?`, id)
	q, err := scanAgentQuestion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentQuestion{}, fmt.Errorf("agent question %q: %w", id, ErrNotFound)
		}
		return AgentQuestion{}, err
	}
	return q, nil
}

// PendingAgentQuestion returns the newest pending question for a goal or
// schedule, if any.
func (s *Store) PendingAgentQuestion(ctx context.Context, origin AgentQuestionOrigin, refID string) (AgentQuestion, error) {
	row := s.db.QueryRowContext(ctx, agentQuestionSelect+`
		WHERE origin = ? AND ref_id = ? AND status = 'pending'
		ORDER BY created_at DESC, id DESC LIMIT 1`, origin, refID)
	q, err := scanAgentQuestion(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentQuestion{}, fmt.Errorf("pending agent question for %s %q: %w", origin, refID, ErrNotFound)
		}
		return AgentQuestion{}, err
	}
	return q, nil
}

// ListPendingAgentQuestions returns pending questions for one origin across all
// refs, newest first.
func (s *Store) ListPendingAgentQuestions(ctx context.Context, origin AgentQuestionOrigin) ([]AgentQuestion, error) {
	rows, err := s.db.QueryContext(ctx, agentQuestionSelect+`
		WHERE origin = ? AND status = 'pending' ORDER BY created_at DESC, id DESC`, origin)
	if err != nil {
		return nil, fmt.Errorf("list pending agent questions for %s: %w", origin, err)
	}
	defer rows.Close()
	return scanAgentQuestions(rows)
}

// ListAnsweredAgentQuestions returns the most recently answered questions for a
// goal or schedule, newest first, so the next run can act on the answers.
func (s *Store) ListAnsweredAgentQuestions(ctx context.Context, origin AgentQuestionOrigin, refID string, limit int) ([]AgentQuestion, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(ctx, agentQuestionSelect+`
		WHERE origin = ? AND ref_id = ? AND status = 'answered'
		ORDER BY answered_at DESC, id DESC LIMIT ?`, origin, refID, limit)
	if err != nil {
		return nil, fmt.Errorf("list answered agent questions for %s %q: %w", origin, refID, err)
	}
	defer rows.Close()
	return scanAgentQuestions(rows)
}

// AnswerAgentQuestion records the user's answers and marks the question answered.
func (s *Store) AnswerAgentQuestion(ctx context.Context, id string, answers map[string][]string) (AgentQuestion, error) {
	if answers == nil {
		answers = map[string][]string{}
	}
	answersJSON, err := json.Marshal(answers)
	if err != nil {
		return AgentQuestion{}, fmt.Errorf("marshal agent question answers: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agent_questions
		SET status = 'answered', answers_json = ?, answered_at = datetime('now')
		WHERE id = ? AND status = 'pending'`, string(answersJSON), id)
	if err != nil {
		return AgentQuestion{}, fmt.Errorf("answer agent question %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return AgentQuestion{}, fmt.Errorf("answer agent question %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return AgentQuestion{}, fmt.Errorf("agent question %q: %w", id, ErrNotFound)
	}
	return s.GetAgentQuestion(ctx, id)
}

// DeleteAgentQuestions removes every question for a goal or schedule. Called
// when the parent goal or schedule file is deleted (this table has no FK to a
// schedule file, and goals must not cascade-delete their tasks).
func (s *Store) DeleteAgentQuestions(ctx context.Context, origin AgentQuestionOrigin, refID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM agent_questions WHERE origin = ? AND ref_id = ?`, origin, refID); err != nil {
		return fmt.Errorf("delete agent questions for %s %q: %w", origin, refID, err)
	}
	return nil
}

const agentQuestionSelect = `SELECT id, origin, ref_id, session_id, questions_json, status,
	answers_json, created_at, COALESCE(answered_at, '')
	FROM agent_questions`

func scanAgentQuestions(rows *sql.Rows) ([]AgentQuestion, error) {
	var out []AgentQuestion
	for rows.Next() {
		q, err := scanAgentQuestion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

func scanAgentQuestion(row scanner) (AgentQuestion, error) {
	var (
		q             AgentQuestion
		questionsJSON string
		answersJSON   string
	)
	if err := row.Scan(
		&q.ID,
		&q.Origin,
		&q.RefID,
		&q.SessionID,
		&questionsJSON,
		&q.Status,
		&answersJSON,
		&q.CreatedAt,
		&q.AnsweredAt,
	); err != nil {
		return AgentQuestion{}, err
	}
	if questionsJSON != "" {
		if err := json.Unmarshal([]byte(questionsJSON), &q.Questions); err != nil {
			return AgentQuestion{}, fmt.Errorf("unmarshal agent question items %q: %w", q.ID, err)
		}
	}
	if answersJSON != "" {
		if err := json.Unmarshal([]byte(answersJSON), &q.Answers); err != nil {
			return AgentQuestion{}, fmt.Errorf("unmarshal agent question answers %q: %w", q.ID, err)
		}
	}
	return q, nil
}
