package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// CreateAccessRequest files a capability request in the pending state. If ID is
// empty a UUID is assigned.
func (s *Store) CreateAccessRequest(ctx context.Context, req AccessRequest) (AccessRequest, error) {
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	if req.Status == "" {
		req.Status = AccessPending
	}
	if req.Payload == "" {
		req.Payload = "{}"
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO access_requests
		(id, goal_id, agent_name, session_id, kind, payload_json, reason, status)
		VALUES (?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?)`,
		req.ID, req.GoalID, req.AgentName, req.SessionID, req.Kind, req.Payload, req.Reason, req.Status,
	)
	if err != nil {
		return AccessRequest{}, fmt.Errorf("create access request %q: %w", req.ID, err)
	}
	return s.GetAccessRequest(ctx, req.ID)
}

// GetAccessRequest fetches an access request by ID.
func (s *Store) GetAccessRequest(ctx context.Context, id string) (AccessRequest, error) {
	row := s.db.QueryRowContext(ctx, accessRequestSelect+` WHERE id = ?`, id)
	req, err := scanAccessRequest(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AccessRequest{}, fmt.Errorf("access request %q: %w", id, ErrNotFound)
		}
		return AccessRequest{}, err
	}
	return req, nil
}

// ListAccessRequests returns access requests newest first, optionally filtered
// by goal and/or status.
func (s *Store) ListAccessRequests(ctx context.Context, goalID, status string) ([]AccessRequest, error) {
	query := accessRequestSelect
	var conds []string
	var args []any
	if goalID != "" {
		conds = append(conds, `goal_id = ?`)
		args = append(args, goalID)
	}
	if status != "" {
		conds = append(conds, `status = ?`)
		args = append(args, status)
	}
	for i, c := range conds {
		if i == 0 {
			query += ` WHERE ` + c
		} else {
			query += ` AND ` + c
		}
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list access requests: %w", err)
	}
	defer rows.Close()

	var reqs []AccessRequest
	for rows.Next() {
		req, err := scanAccessRequest(rows)
		if err != nil {
			return nil, err
		}
		reqs = append(reqs, req)
	}
	return reqs, rows.Err()
}

// DecideAccessRequest records the user's decision (approved or denied) plus an
// optional note that is relayed to the agent at its next review. Decisions are
// accepted only from pending — or from failed, so a request whose automatic
// grant errored stays retryable.
func (s *Store) DecideAccessRequest(ctx context.Context, id string, status AccessRequestStatus, note string) (AccessRequest, error) {
	if status != AccessApproved && status != AccessDenied {
		return AccessRequest{}, fmt.Errorf("decide access request %q: status must be approved or denied, got %q", id, status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE access_requests
		SET status = ?, decision_note = ?, execution_error = '', decided_at = datetime('now')
		WHERE id = ? AND status IN ('pending', 'failed')`, status, note, id)
	if err != nil {
		return AccessRequest{}, fmt.Errorf("decide access request %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return AccessRequest{}, fmt.Errorf("decide access request %q rows affected: %w", id, err)
	}
	if changed == 0 {
		// Distinguish "gone" from "already decided" for a useful error.
		if _, getErr := s.GetAccessRequest(ctx, id); getErr != nil {
			return AccessRequest{}, getErr
		}
		return AccessRequest{}, fmt.Errorf("access request %q: %w", id, ErrAlreadyDecided)
	}
	return s.GetAccessRequest(ctx, id)
}

// MarkAccessRequestExecuted records the outcome of grant execution for an
// automatable kind: executed on success, failed (with the error) otherwise.
// Only an approved request can be marked.
func (s *Store) MarkAccessRequestExecuted(ctx context.Context, id, execErr string) (AccessRequest, error) {
	status := AccessExecuted
	if execErr != "" {
		status = AccessFailed
	}
	res, err := s.db.ExecContext(ctx, `UPDATE access_requests
		SET status = ?, execution_error = ?, executed_at = datetime('now')
		WHERE id = ? AND status = 'approved'`, status, execErr, id)
	if err != nil {
		return AccessRequest{}, fmt.Errorf("mark access request %q executed: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return AccessRequest{}, fmt.Errorf("mark access request %q executed rows affected: %w", id, err)
	}
	if changed == 0 {
		if _, getErr := s.GetAccessRequest(ctx, id); getErr != nil {
			return AccessRequest{}, getErr
		}
		return AccessRequest{}, fmt.Errorf("access request %q is not approved: %w", id, ErrAlreadyDecided)
	}
	return s.GetAccessRequest(ctx, id)
}

// ErrAlreadyDecided marks a decision attempt on an access request that is no
// longer in a decidable state (double-approve, deny-after-execute, …).
var ErrAlreadyDecided = errors.New("already decided")

const accessRequestSelect = `SELECT id, goal_id, agent_name, COALESCE(session_id, ''), kind, payload_json, reason,
	status, decision_note, execution_error, created_at, COALESCE(decided_at, ''), COALESCE(executed_at, '') FROM access_requests`

func scanAccessRequest(row scanner) (AccessRequest, error) {
	var req AccessRequest
	if err := row.Scan(
		&req.ID,
		&req.GoalID,
		&req.AgentName,
		&req.SessionID,
		&req.Kind,
		&req.Payload,
		&req.Reason,
		&req.Status,
		&req.DecisionNote,
		&req.ExecutionError,
		&req.CreatedAt,
		&req.DecidedAt,
		&req.ExecutedAt,
	); err != nil {
		return AccessRequest{}, err
	}
	return req, nil
}
