package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// UpsertNotificationDevice registers a device or refreshes its push token.
//
// Idempotent on the Podiom device id, so the mobile app can call it on every
// launch, on every token refresh, and on every reconnect without accumulating rows.
//
// It first releases the token from any other device row. The push service reassigns
// a token to a different install after a reinstall or a device restore, and leaving
// the stale row in place would send every notification to that phone twice.
func (s *Store) UpsertNotificationDevice(ctx context.Context, d NotificationDevice) (NotificationDevice, error) {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.PushToken == "" {
		return NotificationDevice{}, fmt.Errorf("device %q: push token is required", d.ID)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return NotificationDevice{}, fmt.Errorf("register device %q: %w", d.ID, err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM notification_devices WHERE push_token = ? AND id != ?`,
		d.PushToken, d.ID); err != nil {
		return NotificationDevice{}, fmt.Errorf("release token from other devices: %w", err)
	}
	// `enabled` is deliberately absent from both the insert and the update: a new
	// device takes the column default, and re-registering an existing one leaves it
	// alone. Otherwise the app's next launch would silently un-mute a device the
	// user had switched off.
	//
	// `status` is reset to active, which is the opposite case: a fresh token is exactly
	// what revives a device the relay had reported as gone, and the relay does the same
	// thing on its side for the same reason.
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_devices
		(id, platform, label, push_token, app_version, status)
		VALUES (?, ?, ?, ?, ?, 'active')
		ON CONFLICT(id) DO UPDATE SET
			platform = excluded.platform,
			label = excluded.label,
			push_token = excluded.push_token,
			app_version = excluded.app_version,
			status = 'active',
			updated_at = datetime('now'),
			last_seen_at = datetime('now')`,
		d.ID, d.Platform, d.Label, d.PushToken, d.AppVersion); err != nil {
		return NotificationDevice{}, fmt.Errorf("register device %q: %w", d.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return NotificationDevice{}, fmt.Errorf("register device %q: %w", d.ID, err)
	}
	return s.GetNotificationDevice(ctx, d.ID)
}

// GetNotificationDevice fetches one registered device.
func (s *Store) GetNotificationDevice(ctx context.Context, id string) (NotificationDevice, error) {
	row := s.db.QueryRowContext(ctx, notificationDeviceSelect+` WHERE id = ?`, id)
	device, err := scanNotificationDevice(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return NotificationDevice{}, fmt.Errorf("device %q: %w", id, ErrNotFound)
		}
		return NotificationDevice{}, err
	}
	return device, nil
}

// ListNotificationDevices returns registered devices, newest registration first.
// enabledOnly narrows to the devices that should currently receive notifications.
func (s *Store) ListNotificationDevices(ctx context.Context, enabledOnly bool) ([]NotificationDevice, error) {
	query := notificationDeviceSelect
	if enabledOnly {
		query += ` WHERE enabled = 1`
	}
	rows, err := s.db.QueryContext(ctx, query+` ORDER BY created_at DESC, rowid DESC`)
	if err != nil {
		return nil, fmt.Errorf("list notification devices: %w", err)
	}
	defer rows.Close()
	var out []NotificationDevice
	for rows.Next() {
		device, err := scanNotificationDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, device)
	}
	return out, rows.Err()
}

// SetNotificationDeviceEnabled turns delivery to one device on or off.
//
// This is registration state — "should this phone receive anything" — and is
// deliberately separate from notification preferences, which answer "which events
// matter". Conflating them would make muting one device silently change what every
// device receives.
func (s *Store) SetNotificationDeviceEnabled(ctx context.Context, id string, enabled bool) (NotificationDevice, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE notification_devices SET enabled = ?, updated_at = datetime('now')
		 WHERE id = ?`, boolToInt(enabled), id)
	if err != nil {
		return NotificationDevice{}, fmt.Errorf("set device %q enabled: %w", id, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return NotificationDevice{}, fmt.Errorf("set device %q enabled rows affected: %w", id, err)
	}
	if changed == 0 {
		return NotificationDevice{}, fmt.Errorf("device %q: %w", id, ErrNotFound)
	}
	return s.GetNotificationDevice(ctx, id)
}

// SetNotificationDeviceStatus records what the last delivery attempt learned about a
// device.
//
// This is delivery health, not the user's mute: a device can be enabled and invalid at
// once. Keeping the row rather than deleting it is what preserves the mute choice and the
// label across a token rotation.
func (s *Store) SetNotificationDeviceStatus(ctx context.Context, id, status string) error {
	if status != NotificationDeviceActive && status != NotificationDeviceInvalid {
		return fmt.Errorf("device %q: unknown status %q", id, status)
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE notification_devices SET status = ?, updated_at = datetime('now')
		 WHERE id = ?`, status, id); err != nil {
		return fmt.Errorf("set device %q status: %w", id, err)
	}
	return nil
}

// DeleteNotificationDevice removes a registration. Idempotent.
func (s *Store) DeleteNotificationDevice(ctx context.Context, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM notification_devices WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete device %q: %w", id, err)
	}
	return nil
}

// DeleteNotificationDeviceByToken prunes a device the push service reported as
// unregistered — the app was deleted, or the token was permanently revoked.
//
// This is the native counterpart of the Web Push channel dropping a subscription on
// 404/410: without it, every notification would keep paying for a destination that
// can never be reached again.
func (s *Store) DeleteNotificationDeviceByToken(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM notification_devices WHERE push_token = ?`, token); err != nil {
		return fmt.Errorf("delete device by token: %w", err)
	}
	return nil
}

const notificationDeviceSelect = `SELECT id, platform, label, push_token, app_version,
	enabled, status, created_at, updated_at, last_seen_at FROM notification_devices`

func scanNotificationDevice(row scanner) (NotificationDevice, error) {
	var d NotificationDevice
	if err := row.Scan(
		&d.ID,
		&d.Platform,
		&d.Label,
		&d.PushToken,
		&d.AppVersion,
		&d.Enabled,
		&d.Status,
		&d.CreatedAt,
		&d.UpdatedAt,
		&d.LastSeenAt,
	); err != nil {
		return NotificationDevice{}, err
	}
	return d, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
