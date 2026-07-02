package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Podiom/Podiom/internal/store"
)

var errEmptyEndpoint = errors.New("subscription endpoint is required")

// browserPushSubscription mirrors the JSON produced by the browser's
// PushSubscription.toJSON(). We persist the whole object as the row payload so
// the Web Push channel can decode it straight into webpush.Subscription.
type browserPushSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

// handlePushVAPID serves the VAPID public key the browser needs to subscribe.
func (s *Server) handlePushVAPID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]string{"public_key": s.vapidPublic}, nil)
}

// handlePushSubscribe registers (or refreshes) a browser Web Push subscription.
func (s *Server) handlePushSubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := readRawSubscription(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.core.Store().UpsertPushSubscription(r.Context(), store.PushSubscription{
		Kind:     "webpush",
		Endpoint: body.parsed.Endpoint,
		Payload:  body.raw,
	}); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true}, nil)
}

// handlePushUnsubscribe removes a Web Push subscription by endpoint.
func (s *Server) handlePushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := readRawSubscription(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.core.Store().DeletePushSubscriptionByEndpoint(r.Context(), body.parsed.Endpoint); err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true}, nil)
}

type rawSubscription struct {
	raw    string
	parsed browserPushSubscription
}

// readRawSubscription decodes the request body into both its raw JSON (persisted
// as-is) and a parsed form (for the endpoint identity), validating the shape.
func readRawSubscription(r *http.Request) (rawSubscription, error) {
	var parsed browserPushSubscription
	dec := json.NewDecoder(r.Body)
	// Capture the raw bytes by re-encoding the decoded value; the browser sends a
	// compact, well-defined object so a round-trip is faithful and normalizes it.
	if err := dec.Decode(&parsed); err != nil {
		return rawSubscription{}, err
	}
	if strings.TrimSpace(parsed.Endpoint) == "" {
		return rawSubscription{}, errEmptyEndpoint
	}
	raw, err := json.Marshal(parsed)
	if err != nil {
		return rawSubscription{}, err
	}
	return rawSubscription{raw: string(raw), parsed: parsed}, nil
}
