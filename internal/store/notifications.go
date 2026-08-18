package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// CreateNotification records something meaningful that happened. Notifications are
// always created unread and unresolved; only the user's attention and the
// underlying domain object move them out of those states.
func (s *Store) CreateNotification(ctx context.Context, n Notification) (Notification, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.Importance == "" {
		n.Importance = NotificationNormal
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notifications
		(id, type, category, importance, title, body, agent_name, session_id, goal_id,
		 schedule_name, task_id, resource_kind, resource_id, nav_target, actionable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.ID, n.Type, n.Category, n.Importance, n.Title, n.Body, n.AgentName,
		n.SessionID, n.GoalID, n.ScheduleName, n.TaskID,
		n.ResourceKind, n.ResourceID, n.NavTarget, n.Actionable); err != nil {
		return Notification{}, fmt.Errorf("create notification %q: %w", n.Type, err)
	}
	return s.GetNotification(ctx, n.ID)
}

// GetNotification fetches one notification by id.
func (s *Store) GetNotification(ctx context.Context, id string) (Notification, error) {
	row := s.db.QueryRowContext(ctx, notificationSelect+` WHERE id = ?`, id)
	n, err := scanNotification(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, fmt.Errorf("notification %q: %w", id, ErrNotFound)
		}
		return Notification{}, err
	}
	return n, nil
}

// NotificationFilter narrows the notification list. The zero value returns
// everything, newest first.
type NotificationFilter struct {
	UnreadOnly     bool
	UnresolvedOnly bool
	Category       string
	Limit          int
	Offset         int
}

// where renders the filter as a SQL predicate plus its arguments. Shared by the
// list and count queries so the two can never disagree about what they cover.
func (f NotificationFilter) where() (string, []any) {
	var clauses []string
	var args []any
	if f.UnreadOnly {
		clauses = append(clauses, `read_at IS NULL`)
	}
	if f.UnresolvedOnly {
		clauses = append(clauses, `resolved_at IS NULL AND actionable = 1`)
	}
	if f.Category != "" {
		clauses = append(clauses, `category = ?`)
		args = append(args, f.Category)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	return ` WHERE ` + strings.Join(clauses, ` AND `), args
}

// ListNotifications returns notifications newest first for the Notification
// Center. created_at has one-second resolution, so rowid is the tiebreaker —
// without it, notifications recorded in the same second come back in arbitrary
// order and paging over them can repeat or skip rows.
func (s *Store) ListNotifications(ctx context.Context, f NotificationFilter) ([]Notification, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	where, args := f.where()
	args = append(args, limit, offset)
	rows, err := s.db.QueryContext(ctx, notificationSelect+where+`
		ORDER BY created_at DESC, rowid DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list notifications: %w", err)
	}
	defer rows.Close()
	return scanNotifications(rows)
}

// CountNotifications returns how many notifications match the filter, for the
// unread badge and for paging.
func (s *Store) CountNotifications(ctx context.Context, f NotificationFilter) (int, error) {
	where, args := f.where()
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications`+where, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return n, nil
}

// CountAttentionNotifications counts unread notifications that are worth
// interrupting for: those the registry marks important or critical.
//
// This is what a badge should show. Counting every unread row would mean a badge that
// is permanently non-zero on any busy installation, driven by routine progress and run
// activity — which makes it useless for its one job of saying "something needs you".
// The plain unread count is still reported alongside it for the list itself.
func (s *Store) CountAttentionNotifications(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM notifications
		 WHERE read_at IS NULL AND importance IN ('important', 'critical')`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count attention notifications: %w", err)
	}
	return n, nil
}

// SetNotificationRead marks a notification seen (or puts it back to unread).
// Guarded on the current state so read_at is stamped once: a second call is
// ErrNotFound rather than a silent re-stamp that would reorder the user's list.
//
// This never touches resolved_at and never touches the domain object behind the
// notification — seeing that an agent wants permission is not granting it.
func (s *Store) SetNotificationRead(ctx context.Context, id string, read bool) (Notification, error) {
	stmt := `UPDATE notifications SET read_at = datetime('now') WHERE id = ? AND read_at IS NULL`
	if !read {
		stmt = `UPDATE notifications SET read_at = NULL WHERE id = ? AND read_at IS NOT NULL`
	}
	res, err := s.db.ExecContext(ctx, stmt, id)
	if err != nil {
		return Notification{}, fmt.Errorf("set notification %q read: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Notification{}, fmt.Errorf("set notification %q read rows affected: %w", id, err)
	}
	if changed == 0 {
		return Notification{}, fmt.Errorf("notification %q in the expected read state: %w", id, ErrNotFound)
	}
	return s.GetNotification(ctx, id)
}

// MarkAllNotificationsRead clears the unread badge in one statement and reports
// how many rows it changed.
func (s *Store) MarkAllNotificationsRead(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET read_at = datetime('now') WHERE read_at IS NULL`)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read rows affected: %w", err)
	}
	return int(changed), nil
}

// ResolveNotification records that the actionable condition behind a notification
// has been handled. Guarded on the unresolved state so the first resolution wins.
func (s *Store) ResolveNotification(ctx context.Context, id string) (Notification, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET resolved_at = datetime('now')
		 WHERE id = ? AND resolved_at IS NULL`, id)
	if err != nil {
		return Notification{}, fmt.Errorf("resolve notification %q: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Notification{}, fmt.Errorf("resolve notification %q rows affected: %w", id, err)
	}
	if changed == 0 {
		return Notification{}, fmt.Errorf("unresolved notification %q: %w", id, ErrNotFound)
	}
	return s.GetNotification(ctx, id)
}

// ResolveNotificationsByResource resolves every unresolved notification about one
// domain object. This is how notification state follows the domain: answering a
// question in the dashboard resolves the notification about it on every device,
// no matter which surface the answer came from.
//
// Returns the rows it changed so the caller can broadcast them.
func (s *Store) ResolveNotificationsByResource(ctx context.Context, kind, id string) ([]Notification, error) {
	if kind == "" || id == "" {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, notificationSelect+`
		WHERE resource_kind = ? AND resource_id = ? AND resolved_at IS NULL`, kind, id)
	if err != nil {
		return nil, fmt.Errorf("find notifications for %s %q: %w", kind, id, err)
	}
	pending, err := scanNotifications(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notifications SET resolved_at = datetime('now')
		 WHERE resource_kind = ? AND resource_id = ? AND resolved_at IS NULL`, kind, id); err != nil {
		return nil, fmt.Errorf("resolve notifications for %s %q: %w", kind, id, err)
	}
	out := make([]Notification, 0, len(pending))
	for _, n := range pending {
		fresh, err := s.GetNotification(ctx, n.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, fresh)
	}
	return out, nil
}

// FindUnresolvedNotification returns an existing open notification of this type
// about this domain object, so a producer that fires twice for the same still-open
// request does not stack up duplicates on the user's phone.
func (s *Store) FindUnresolvedNotification(ctx context.Context, notifType, kind, id string) (Notification, error) {
	row := s.db.QueryRowContext(ctx, notificationSelect+`
		WHERE type = ? AND resource_kind = ? AND resource_id = ? AND resolved_at IS NULL
		ORDER BY created_at DESC, rowid DESC LIMIT 1`, notifType, kind, id)
	n, err := scanNotification(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Notification{}, fmt.Errorf("unresolved %s for %s %q: %w", notifType, kind, id, ErrNotFound)
		}
		return Notification{}, err
	}
	return n, nil
}

// PruneNotifications keeps the newest `keep` notifications and deletes the rest,
// their deliveries cascading. History is worth keeping; unbounded history is not.
func (s *Store) PruneNotifications(ctx context.Context, keep int) (int, error) {
	if keep <= 0 {
		return 0, nil
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE id IN (
		SELECT id FROM notifications ORDER BY created_at DESC, rowid DESC LIMIT -1 OFFSET ?
	)`, keep)
	if err != nil {
		return 0, fmt.Errorf("prune notifications: %w", err)
	}
	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune notifications rows affected: %w", err)
	}
	return int(deleted), nil
}

// CreateNotificationDelivery records that a channel is about to attempt delivery.
func (s *Store) CreateNotificationDelivery(ctx context.Context, d NotificationDelivery) (NotificationDelivery, error) {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = NotificationDeliveryPending
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notification_deliveries
		(id, notification_id, channel, destination, status, error)
		VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.NotificationID, d.Channel, d.Destination, d.Status, d.Error); err != nil {
		return NotificationDelivery{}, fmt.Errorf("create %s delivery for notification %q: %w",
			d.Channel, d.NotificationID, err)
	}
	return s.getNotificationDelivery(ctx, d.ID)
}

// FinishNotificationDelivery records the outcome of an attempt. destination is
// set here because a channel only learns where it delivered by trying.
func (s *Store) FinishNotificationDelivery(ctx context.Context, id, destination string, status NotificationDeliveryStatus, errMsg string) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE notification_deliveries
		SET destination = ?, status = ?, error = ?, attempted_at = datetime('now')
		WHERE id = ?`, destination, status, errMsg, id); err != nil {
		return fmt.Errorf("finish delivery %q: %w", id, err)
	}
	return nil
}

// ListNotificationDeliveries returns every attempt made for one notification,
// oldest first.
func (s *Store) ListNotificationDeliveries(ctx context.Context, notificationID string) ([]NotificationDelivery, error) {
	rows, err := s.db.QueryContext(ctx, notificationDeliverySelect+`
		WHERE notification_id = ? ORDER BY attempted_at, rowid`, notificationID)
	if err != nil {
		return nil, fmt.Errorf("list deliveries for notification %q: %w", notificationID, err)
	}
	defer rows.Close()
	var out []NotificationDelivery
	for rows.Next() {
		d, err := scanNotificationDelivery(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListNotificationPreferences returns the user's explicit overrides. Types with
// no row here fall back to their registry default.
func (s *Store) ListNotificationPreferences(ctx context.Context) ([]NotificationPreference, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT type, channel, enabled, updated_at FROM notification_preferences
		 ORDER BY type, channel`)
	if err != nil {
		return nil, fmt.Errorf("list notification preferences: %w", err)
	}
	defer rows.Close()
	var out []NotificationPreference
	for rows.Next() {
		var p NotificationPreference
		if err := rows.Scan(&p.Type, &p.Channel, &p.Enabled, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SetNotificationPreference records an explicit choice for one (type, channel).
func (s *Store) SetNotificationPreference(ctx context.Context, p NotificationPreference) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO notification_preferences
		(type, channel, enabled) VALUES (?, ?, ?)
		ON CONFLICT(type, channel) DO UPDATE SET
			enabled = excluded.enabled, updated_at = datetime('now')`,
		p.Type, p.Channel, p.Enabled); err != nil {
		return fmt.Errorf("set notification preference %s/%s: %w", p.Type, p.Channel, err)
	}
	return nil
}

func (s *Store) getNotificationDelivery(ctx context.Context, id string) (NotificationDelivery, error) {
	row := s.db.QueryRowContext(ctx, notificationDeliverySelect+` WHERE id = ?`, id)
	d, err := scanNotificationDelivery(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NotificationDelivery{}, fmt.Errorf("delivery %q: %w", id, ErrNotFound)
		}
		return NotificationDelivery{}, err
	}
	return d, nil
}

const notificationSelect = `SELECT id, type, category, importance, title, body,
	agent_name, session_id, goal_id, schedule_name, task_id,
	resource_kind, resource_id, nav_target, actionable, created_at,
	COALESCE(read_at, ''), COALESCE(resolved_at, '')
	FROM notifications`

const notificationDeliverySelect = `SELECT id, notification_id, channel, destination,
	status, error, attempted_at FROM notification_deliveries`

func scanNotifications(rows *sql.Rows) ([]Notification, error) {
	var out []Notification
	for rows.Next() {
		n, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func scanNotification(row scanner) (Notification, error) {
	var n Notification
	if err := row.Scan(
		&n.ID,
		&n.Type,
		&n.Category,
		&n.Importance,
		&n.Title,
		&n.Body,
		&n.AgentName,
		&n.SessionID,
		&n.GoalID,
		&n.ScheduleName,
		&n.TaskID,
		&n.ResourceKind,
		&n.ResourceID,
		&n.NavTarget,
		&n.Actionable,
		&n.CreatedAt,
		&n.ReadAt,
		&n.ResolvedAt,
	); err != nil {
		return Notification{}, err
	}
	return n, nil
}

func scanNotificationDelivery(row scanner) (NotificationDelivery, error) {
	var d NotificationDelivery
	if err := row.Scan(
		&d.ID,
		&d.NotificationID,
		&d.Channel,
		&d.Destination,
		&d.Status,
		&d.Error,
		&d.AttemptedAt,
	); err != nil {
		return NotificationDelivery{}, err
	}
	return d, nil
}
