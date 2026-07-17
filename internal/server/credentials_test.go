package server

import (
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

	// Non-GET on the collection is rejected.
	req = httptest.NewRequest(http.MethodPost, "/api/credentials", nil)
	rr = httptest.NewRecorder()
	srv.handleCredentials(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("post: %d, want 405", rr.Code)
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
