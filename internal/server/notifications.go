package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/Podiom/Podiom/internal/notify"
	"github.com/Podiom/Podiom/internal/store"
)

// notificationListResponse is the Notification Center's page of history.
//
// Unread rides along with the page so the badge and the list come from one
// request: fetching them separately lets them disagree, which shows up as a badge
// counting notifications the list is not displaying.
type notificationListResponse struct {
	Notifications []notificationView `json:"notifications"`
	Unread        int                `json:"unread"`
	// Attention is the unread count that is worth interrupting for — important and
	// critical only. It is what a badge should display: counting every unread row would
	// leave the badge permanently lit by routine progress and run activity, and stop it
	// meaning anything.
	Attention int `json:"attention"`
	Total     int `json:"total"`
}

// notificationView is a stored notification plus the actions valid right now.
//
// Actions are attached per response rather than stored on the row, because what a
// notification can do depends on domain state that keeps moving after it was
// recorded.
type notificationView struct {
	store.Notification
	Actions []notify.Action `json:"actions"`
}

// notificationPreferencesResponse carries the whole preference model, so the
// settings UI renders from the server rather than from hardcoded client tables.
type notificationPreferencesResponse struct {
	Groups []notify.PreferenceGroup `json:"groups"`
}

type notificationPreferencesRequest struct {
	Preferences []notify.PreferenceUpdate `json:"preferences"`
}

// handleNotifications serves the Notification Center list at /api/notifications.
func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	query := r.URL.Query()
	filter := store.NotificationFilter{
		UnreadOnly:     query.Get("unread") == "1",
		UnresolvedOnly: query.Get("unresolved") == "1",
		Category:       strings.TrimSpace(query.Get("category")),
		Limit:          atoiDefault(query.Get("limit"), 50),
		Offset:         atoiDefault(query.Get("offset"), 0),
	}

	db := s.core.Store()
	rows, err := db.ListNotifications(r.Context(), filter)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	total, err := db.CountNotifications(r.Context(), filter)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	unread, err := db.CountNotifications(r.Context(), store.NotificationFilter{UnreadOnly: true})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	attention, err := db.CountAttentionNotifications(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	views := make([]notificationView, 0, len(rows))
	for _, row := range rows {
		views = append(views, s.notificationView(r, row))
	}
	writeJSON(w, notificationListResponse{
		Notifications: views, Unread: unread, Attention: attention, Total: total,
	}, nil)
}

// handleNotificationsReadAll clears the unread badge in one call.
func (s *Server) handleNotificationsReadAll(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	updated, err := s.core.Store().MarkAllNotificationsRead(r.Context())
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	// Every open client re-reads rather than being sent all of them: a
	// mark-everything-read can touch hundreds of rows, and broadcasting each one
	// would be far more traffic than telling clients to refresh.
	s.broadcastWS(ServerMessage{Type: "notifications_read_all"})
	writeJSON(w, map[string]int{"updated": updated}, nil)
}

// handleNotificationPreferences reads and writes which events notify the user.
func (s *Server) handleNotificationPreferences(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	switch r.Method {
	case http.MethodGet:
		groups, err := s.notifications.PreferenceGroups(r.Context())
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, notificationPreferencesResponse{Groups: groups}, nil)
	case http.MethodPut:
		var req notificationPreferencesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := s.notifications.SetPreferences(r.Context(), req.Preferences); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		groups, err := s.notifications.PreferenceGroups(r.Context())
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, notificationPreferencesResponse{Groups: groups}, nil)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleNotification serves one notification and its state changes at
// /api/notifications/{id}[/{action}].
func (s *Server) handleNotification(w http.ResponseWriter, r *http.Request) {
	if s.core == nil {
		http.Error(w, "core unavailable", http.StatusServiceUnavailable)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/notifications/")
	id, action, _ := strings.Cut(rest, "/")
	if id == "" {
		http.Error(w, "notification id is required", http.StatusBadRequest)
		return
	}
	if actionID, ok := strings.CutPrefix(action, "actions/"); ok {
		s.handleNotificationAction(w, r, id, actionID)
		return
	}

	db := s.core.Store()
	switch action {
	case "":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		row, err := db.GetNotification(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, s.notificationView(r, row), nil)
	case "read", "unread":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		row, err := db.SetNotificationRead(r.Context(), id, action == "read")
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.broadcastNotificationUpdate(row)
		writeJSON(w, s.notificationView(r, row), nil)
	case "resolve":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// Resolving the notification only says the user has dealt with it. It does
		// not touch the domain object — that is what the action endpoint is for, and
		// conflating the two would let dismissing a prompt look like answering it.
		row, err := db.ResolveNotification(r.Context(), id)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		s.broadcastNotificationUpdate(row)
		writeJSON(w, s.notificationView(r, row), nil)
	default:
		http.Error(w, "unknown notification action", http.StatusNotFound)
	}
}

// broadcastNotification tells every live client about a newly recorded
// notification.
func (s *Server) broadcastNotification(n store.Notification) {
	s.broadcastWS(ServerMessage{Type: "notification", Notification: &n})
}

// BroadcastNotification is the exported hook the notification engine calls (via
// SetBroadcaster) so a notification reaches open dashboards the moment it is
// recorded, whether or not any external channel is enabled.
func (s *Server) BroadcastNotification(n store.Notification) {
	s.broadcastNotification(n)
}

// broadcastNotificationUpdate tells every live client that a notification's read
// or resolved state changed, so acting on one device updates the others.
func (s *Server) broadcastNotificationUpdate(n store.Notification) {
	s.broadcastWS(ServerMessage{Type: "notification_update", Notification: &n})
}

// BroadcastNotificationUpdate is the exported hook the notification engine calls
// when resolving a domain object clears the notifications about it.
func (s *Server) BroadcastNotificationUpdate(n store.Notification) {
	s.broadcastNotificationUpdate(n)
}

// RequestPending reports whether an in-memory request is still awaiting the user.
//
// Wired into the notification engine so a notification about a permission prompt or
// a live question stops offering actions once it has been answered somewhere else.
// Those requests exist only for the duration of a turn, so the brokers are the only
// place that knows.
func (s *Server) RequestPending(kind notify.ResourceKind, requestID string) bool {
	switch kind {
	case notify.ResourcePermissionRequest:
		return s.broker.isPending(requestID)
	case notify.ResourceSessionQuestion:
		return s.input.isPending(requestID)
	}
	// Anything else is backed by a database row whose own state is authoritative.
	return true
}

// resolveNotificationsFor clears the notifications about one domain object.
//
// Used where a decision does not pass through the goal timeline — the in-memory
// permission and user-input relays — so answering in the dashboard still settles the
// notification on every other device.
func (s *Server) resolveNotificationsFor(r *http.Request, kind notify.ResourceKind, resourceID string) {
	if s.core == nil || resourceID == "" {
		return
	}
	changed, err := s.core.Store().ResolveNotificationsByResource(r.Context(), string(kind), resourceID)
	if err != nil {
		return
	}
	for _, row := range changed {
		s.broadcastNotificationUpdate(row)
	}
}

// notificationView attaches the currently valid actions to a stored notification.
func (s *Server) notificationView(r *http.Request, row store.Notification) notificationView {
	return notificationView{
		Notification: row,
		Actions:      s.notifications.LiveActions(r.Context(), row),
	}
}

// atoiDefault parses a query integer, falling back when it is absent or unusable.
func atoiDefault(raw string, fallback int) int {
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
