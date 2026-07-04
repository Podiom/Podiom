package gateway

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateGeneratesAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")

	k, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected first load to create a token")
	}
	if len(k.Current()) != 43 { // 32 bytes base64url unpadded
		t.Fatalf("token length = %d, want 43", len(k.Current()))
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("token file mode = %o, want 600", perm)
		}
	}

	// A second load returns the same value without regenerating.
	k2, created, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("expected second load to reuse the existing token")
	}
	if k2.Current() != k.Current() {
		t.Fatal("reloaded token differs from generated one")
	}
}

func TestRotateInvalidatesOldToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	k, _, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	old := k.Current()

	rotated, err := k.Rotate()
	if err != nil {
		t.Fatal(err)
	}
	if rotated == old {
		t.Fatal("rotation returned the same token")
	}
	if k.Current() != rotated {
		t.Fatal("keeper did not adopt the rotated token")
	}
	onDisk, err := ReadTokenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if onDisk != rotated {
		t.Fatal("rotated token not persisted to disk")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
	req.Header.Set(Header, old)
	if k.Authorize(req) {
		t.Fatal("old token still authorized after rotation")
	}
}

func TestAuthorizeCarriers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gateway.token")
	k, _, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	token := k.Current()

	cases := []struct {
		name string
		set  func(r *http.Request)
		want bool
	}{
		{"custom header", func(r *http.Request) { r.Header.Set(Header, token) }, true},
		{"bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+token) }, true},
		{"ws subprotocol", func(r *http.Request) {
			r.Header.Set("Sec-WebSocket-Protocol", WSProtocol+", "+WSProtocolEntry(token))
		}, true},
		{"no credentials", func(r *http.Request) {}, false},
		{"wrong header", func(r *http.Request) { r.Header.Set(Header, "nope") }, false},
		{"wrong bearer", func(r *http.Request) { r.Header.Set("Authorization", "Bearer nope") }, false},
		{"wrong subprotocol", func(r *http.Request) {
			r.Header.Set("Sec-WebSocket-Protocol", WSProtocolEntry("nope"))
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/x", nil)
			tc.set(req)
			if got := k.Authorize(req); got != tc.want {
				t.Fatalf("Authorize = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWSProtocolTokenParsing(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/ws", nil)
	if got := WSProtocolToken(req); got != "" {
		t.Fatalf("empty request: got %q", got)
	}
	req.Header.Add("Sec-WebSocket-Protocol", "podiom.v1")
	req.Header.Add("Sec-WebSocket-Protocol", " podiom-token.abc123 , other")
	if got := WSProtocolToken(req); got != "abc123" {
		t.Fatalf("got %q, want abc123", got)
	}
}
