package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSourceGuardPassthroughWhenUnrestricted(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h, err := buildSourceGuard(next, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "203.0.113.10:9999"
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusTeapot {
		t.Fatalf("unrestricted guard blocked: status = %d", rr.Code)
	}
}

func TestSourceGuardHAMode(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h, err := buildSourceGuard(next, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		remote string
		allow  bool
	}{
		{"ingress proxy", "172.30.32.2:41234", true},
		{"loopback ipv4", "127.0.0.1:555", true},
		{"loopback ipv6", "[::1]:555", true},
		{"other ha network host", "172.30.32.3:555", false},
		{"lan host", "192.168.1.20:555", false},
		{"public host", "203.0.113.10:555", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = tc.remote
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if tc.allow && rr.Code != http.StatusTeapot {
				t.Fatalf("status = %d, want pass", rr.Code)
			}
			if !tc.allow && rr.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rr.Code)
			}
		})
	}
}

func TestSourceGuardAllowFromList(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h, err := buildSourceGuard(next, []string{"192.168.1.0/24", "10.0.0.5"}, false)
	if err != nil {
		t.Fatal(err)
	}
	allowed := []string{"192.168.1.42:1", "10.0.0.5:1", "127.0.0.1:1"}
	denied := []string{"192.168.2.1:1", "10.0.0.6:1", "172.30.32.2:1"}
	for _, remote := range allowed {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusTeapot {
			t.Fatalf("%s: status = %d, want pass", remote, rr.Code)
		}
	}
	for _, remote := range denied {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = remote
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", remote, rr.Code)
		}
	}
}

func TestSourceGuardRejectsMalformedEntry(t *testing.T) {
	next := http.NotFoundHandler()
	if _, err := buildSourceGuard(next, []string{"not-an-ip"}, false); err == nil {
		t.Fatal("expected error for malformed allow_from entry")
	}
}
