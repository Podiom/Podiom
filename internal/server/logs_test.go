package server

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
	podiomlog "github.com/Podiom/Podiom/internal/logging"
)

func TestHandleLogsReturnsTailForLoopback(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if err := os.MkdirAll(paths.LogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(podiomlog.Path(paths.LogsDir), []byte("one\ntwo\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Paths: paths})
	req := httptest.NewRequest(http.MethodGet, "/api/logs?lines=2", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	rr := httptest.NewRecorder()

	s.handleLogs(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"two"`) || !strings.Contains(body, `"three"`) || strings.Contains(body, `"one"`) {
		t.Fatalf("unexpected body: %s", body)
	}
}

// Logs used to be gated to loopback callers; that gate is superseded by the
// gateway token, which the full handler chain enforces on every /api/ route.
func TestHandleLogsRequiresGatewayToken(t *testing.T) {
	home := t.TempDir()
	paths := config.NewPaths(home)
	keeper, _, err := gateway.LoadOrCreate(paths.GatewayToken)
	if err != nil {
		t.Fatal(err)
	}
	s := New(Options{Paths: paths, Tokens: keeper})

	req := httptest.NewRequest(http.MethodGet, "/api/logs", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", rr.Code)
	}

	if err := os.MkdirAll(paths.LogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(podiomlog.Path(paths.LogsDir), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodGet, "/api/logs?lines=1", nil)
	req.RemoteAddr = "203.0.113.10:12345"
	req.Header.Set(gateway.Header, keeper.Current())
	rr = httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status with token = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestHandleLogsFollowStreamsNDJSON(t *testing.T) {
	paths := config.NewPaths(t.TempDir())
	if err := os.MkdirAll(paths.LogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(podiomlog.Path(paths.LogsDir), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{Paths: paths})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ts := httptest.NewServer(http.HandlerFunc(s.handleLogsFollow))
	defer ts.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/logs/follow?lines=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("follow request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	linec := make(chan string, 1)
	errc := make(chan error, 1)
	go func() {
		line, err := bufio.NewReader(resp.Body).ReadString('\n')
		if err != nil {
			errc <- err
			return
		}
		linec <- line
	}()
	select {
	case line := <-linec:
		if !strings.Contains(line, `"line":"hello"`) {
			t.Fatalf("unexpected stream line: %s", line)
		}
	case err := <-errc:
		t.Fatalf("read stream: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for stream line")
	}
	cancel()
}
