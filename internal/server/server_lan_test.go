package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartFailsWhenLANListenerCannotBind(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	lanPort := occupied.Addr().(*net.TCPAddr).Port

	s := New(Options{Bind: "127.0.0.1", Port: 0, LANPort: lanPort})
	err = s.Start()
	if err == nil || !strings.Contains(err.Error(), "listen on LAN API") {
		t.Fatalf("Start error = %v, want LAN bind failure", err)
	}
}

func TestStartAndShutdownCoordinateWebAndLANListeners(t *testing.T) {
	webPort := reservePort(t)
	lanPort := reservePort(t)
	s := New(Options{Bind: "127.0.0.1", Port: webPort, LANPort: lanPort})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	errCh := make(chan error, 1)
	go func() { errCh <- s.Start() }()

	for _, port := range []int{webPort, lanPort} {
		url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
		deadline := time.Now().Add(3 * time.Second)
		for {
			resp, err := http.Get(url)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK {
					break
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("listener %d did not become healthy", port)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Start returned after Shutdown: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("Start did not return after both listeners shut down")
	}
}

func reservePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}
