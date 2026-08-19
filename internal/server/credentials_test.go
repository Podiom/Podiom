package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/creds"
)

func TestCredentialsListAndDelete(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if err := srv.core.StoreCredential(context.Background(), creds.Credential{
		Name: "GITHUB_TOKEN", Value: "tok_s3cret", Purpose: "gh API", GoalID: "g1",
	}); err != nil {
		t.Fatalf("store credential: %v", err)
	}

	// List returns metadata only — never the value.
	req := httptest.NewRequest(http.MethodGet, "/api/credentials", nil)
	rr := httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), "tok_s3cret") {
		t.Fatal("credential listing leaks the value")
	}
	var list []credentialView
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(list) != 1 || list[0].Name != "GITHUB_TOKEN" || list[0].Purpose != "gh API" || list[0].GoalID != "g1" {
		t.Fatalf("list = %+v", list)
	}
	if list[0].CreatedAt == "" {
		t.Fatal("created_at missing")
	}

	// PUT on the collection is still rejected; only GET and POST are handled.
	req = httptest.NewRequest(http.MethodPut, "/api/credentials", nil)
	rr = httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("put: %d, want 405", rr.Code)
	}

	// Delete removes the credential; deleting again fails.
	req = httptest.NewRequest(http.MethodDelete, "/api/credentials/GITHUB_TOKEN", nil)
	rr = httptest.NewRecorder()
	srv.handleCredential(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	req = httptest.NewRequest(http.MethodDelete, "/api/credentials/GITHUB_TOKEN", nil)
	rr = httptest.NewRecorder()
	srv.handleCredential(rr, req)
	if rr.Code == http.StatusOK {
		t.Fatal("double delete should fail")
	}

	req = httptest.NewRequest(http.MethodGet, "/api/credentials", nil)
	rr = httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	var after []credentialView
	_ = json.NewDecoder(rr.Body).Decode(&after)
	if len(after) != 0 {
		t.Fatalf("list after delete = %+v", after)
	}
}

// postCredential is the shared driver for the store endpoint: both writers (the
// Settings form and podiom_store_credential) hit exactly this path.
func postCredential(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/credentials", bytes.NewBufferString(body))
	rr := httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	return rr
}

func credentialNames(t *testing.T, srv *Server) []credentialView {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/credentials", nil)
	rr := httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), sentinelSecret) {
		t.Fatal("credential listing leaks the value")
	}
	var list []credentialView
	if err := json.NewDecoder(rr.Body).Decode(&list); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return list
}

// sentinelSecret is distinctive enough that any leak into a response body shows
// up in a substring check.
const sentinelSecret = "sentinel-a1b2c3"

func TestStoreCredentialAttributesAgentAndHidesValue(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	rr := postCredential(t, srv, `{"name":"STRIPE_KEY","value":"`+sentinelSecret+`","purpose":"billing probe","created_by_agent":"atlas","created_by_session":"sess-1"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("store: %d %s", rr.Code, rr.Body.String())
	}
	if strings.Contains(rr.Body.String(), sentinelSecret) {
		t.Fatal("store response echoes the secret value")
	}
	var got credentialView
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "STRIPE_KEY" || got.Purpose != "billing probe" {
		t.Fatalf("store response = %+v", got)
	}
	if got.CreatedByAgent != "atlas" || got.CreatedBySession != "sess-1" {
		t.Fatalf("provenance not recorded: %+v", got)
	}
	if got.CreatedAt == "" {
		t.Fatal("created_at should be stamped by the store")
	}

	// The value really landed: it is what the agent subprocess environment gets.
	list := credentialNames(t, srv)
	if len(list) != 1 || list[0].CreatedByAgent != "atlas" {
		t.Fatalf("listing = %+v", list)
	}
}

func TestStoreCredentialWithoutAgentStaysUnattributed(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	// The Settings form posts no provenance: blank is how the UI shows the user
	// entered it themselves.
	if rr := postCredential(t, srv, `{"name":"USER_TOKEN","value":"typed-by-hand"}`); rr.Code != http.StatusOK {
		t.Fatalf("store: %d %s", rr.Code, rr.Body.String())
	}
	list := credentialNames(t, srv)
	if len(list) != 1 || list[0].CreatedByAgent != "" || list[0].CreatedBySession != "" {
		t.Fatalf("user-entered credential should stay unattributed: %+v", list)
	}
}

func TestStoreCredentialOverwriteGuard(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	if err := srv.core.StoreCredential(context.Background(), creds.Credential{
		Name: "GITHUB_TOKEN", Value: "original", Purpose: "gh API", GoalID: "g1",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Storing over an existing name without the flag is refused, and the
	// original value survives.
	rr := postCredential(t, srv, `{"name":"GITHUB_TOKEN","value":"clobbered"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unguarded overwrite: %d %s, want 400", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "overwrite=true") {
		t.Fatalf("rejection should name the guard: %s", rr.Body.String())
	}
	stored, _ := srv.core.ListCredentials(context.Background())
	if len(stored) != 1 || stored[0].Value != "original" {
		t.Fatalf("refused overwrite must not touch the value: %+v", stored)
	}

	// With the flag it replaces, keeping the goal link and purpose the user's
	// original grant carried.
	rr = postCredential(t, srv, `{"name":"GITHUB_TOKEN","value":"rotated","overwrite":true,"created_by_agent":"atlas"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("guarded overwrite: %d %s", rr.Code, rr.Body.String())
	}
	stored, _ = srv.core.ListCredentials(context.Background())
	if len(stored) != 1 || stored[0].Value != "rotated" {
		t.Fatalf("overwrite did not replace: %+v", stored)
	}
	if stored[0].Purpose != "gh API" || stored[0].GoalID != "g1" {
		t.Fatalf("rotation dropped the original metadata: %+v", stored[0])
	}
}

func TestStoreCredentialValidation(t *testing.T) {
	_, srv, cleanup := newAgentAPITestServer(t)
	defer cleanup()

	cases := []struct{ name, body string }{
		{"missing name", `{"value":"v"}`},
		{"blank name", `{"name":"   ","value":"v"}`},
		{"missing value", `{"name":"TOKEN"}`},
		{"blank value", `{"name":"TOKEN","value":"  "}`},
		{"reserved name", `{"name":"PATH","value":"v"}`},
		{"name is not a bare variable", `{"name":"A=B","value":"v"}`},
	}
	for _, tc := range cases {
		if rr := postCredential(t, srv, tc.body); rr.Code == http.StatusOK {
			t.Errorf("%s: expected rejection, got 200", tc.name)
		}
	}
	if list := credentialNames(t, srv); len(list) != 0 {
		t.Fatalf("nothing should have been stored: %+v", list)
	}

	rr := postCredential(t, srv, `not json`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d, want 400", rr.Code)
	}
}
