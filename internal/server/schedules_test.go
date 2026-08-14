package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Podiom/Podiom/internal/schedule"
	"github.com/Podiom/Podiom/internal/store"
)

// webhookTestSchedule creates a webhook-triggered schedule and returns the
// secret the scheduler minted for it.
func webhookTestSchedule(t *testing.T, sched *schedule.Scheduler, name string) string {
	t.Helper()
	status, err := sched.Create(context.Background(), schedule.CreateParams{
		Name:    name,
		Agent:   "atlas",
		Webhook: true,
		Body:    "React to the push.",
	})
	if err != nil {
		t.Fatalf("create schedule %q: %v", name, err)
	}
	if status.WebhookSecret == "" {
		t.Fatalf("schedule %q was created without a webhook secret", name)
	}
	return status.WebhookSecret
}

func postWebhook(t *testing.T, srv *Server, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	rr := httptest.NewRecorder()
	srv.handleSchedule(rr, req)
	return rr
}

// waitForRun blocks until an accepted webhook run reaches a terminal state. The
// endpoint answers before the session finishes, so without this the run's
// goroutine would still be writing when the test closes the store.
func waitForRun(t *testing.T, sched *schedule.Scheduler, name string) store.ScheduleRun {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		status, err := sched.Status(context.Background(), name)
		if err != nil {
			t.Fatalf("status: %v", err)
		}
		if len(status.Runs) > 0 && status.Runs[0].Status != store.RunRunning {
			return status.Runs[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("webhook run for %q did not finish", name)
	return store.ScheduleRun{}
}

// TestScheduleWebhookAcceptsAndRecordsRun pins the happy path: the endpoint
// answers immediately rather than holding the sender's connection open for the
// length of an agent run, and hands back the run it started.
func TestScheduleWebhookAcceptsAndRecordsRun(t *testing.T) {
	_, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()
	secret := webhookTestSchedule(t, sched, "on-push")

	rr := postWebhook(t, srv, "/api/schedules/on-push/webhook?secret="+secret, `{"event":"push"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
	}
	var run store.ScheduleRun
	if err := json.Unmarshal(rr.Body.Bytes(), &run); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if run.ID == "" || run.Trigger != store.TriggerWebhook {
		t.Fatalf("unexpected run: %+v", run)
	}

	finished := waitForRun(t, sched, "on-push")
	if finished.Status != store.RunSuccess {
		t.Fatalf("run status = %q, want success (%q)", finished.Status, finished.Error)
	}
	if finished.ID != run.ID || finished.SessionID == "" {
		t.Fatalf("the accepted run should be the one that ran: %+v", finished)
	}
}

// TestScheduleWebhookAcceptsSecretFromHeaders pins the two header forms
// alongside the query parameter, since a given sender can usually only produce
// one of the three.
func TestScheduleWebhookAcceptsSecretFromHeaders(t *testing.T) {
	_, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()
	secret := webhookTestSchedule(t, sched, "on-push")

	cases := map[string]map[string]string{
		"dedicated header": {"X-Podiom-Webhook-Secret": secret},
		"bearer token":     {"Authorization": "Bearer " + secret},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/schedules/on-push/webhook", strings.NewReader("{}"))
			for k, v := range headers {
				req.Header.Set(k, v)
			}
			rr := httptest.NewRecorder()
			srv.handleSchedule(rr, req)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202 (body=%s)", rr.Code, rr.Body.String())
			}
			waitForRun(t, sched, "on-push")
		})
	}
}

// TestScheduleWebhookRejectionsAreIndistinguishable pins that the endpoint
// cannot be used to discover which schedules exist. It is reachable without the
// gateway token, so a bad secret, an unknown name, and a schedule that has no
// webhook trigger must all answer identically.
func TestScheduleWebhookRejectionsAreIndistinguishable(t *testing.T) {
	_, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()
	secret := webhookTestSchedule(t, sched, "on-push")
	if _, err := sched.Create(context.Background(), schedule.CreateParams{
		Name:  "clock-only",
		Agent: "atlas",
		Cron:  "0 7 * * *",
		Body:  "Tick.",
	}); err != nil {
		t.Fatalf("create clock-only schedule: %v", err)
	}

	paths := []string{
		"/api/schedules/on-push/webhook?secret=guess",
		"/api/schedules/on-push/webhook",
		"/api/schedules/ghost/webhook?secret=" + secret,
		"/api/schedules/clock-only/webhook?secret=" + secret,
	}
	var bodies []string
	for _, path := range paths {
		rr := postWebhook(t, srv, path, "{}")
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401 (body=%s)", path, rr.Code, rr.Body.String())
		}
		bodies = append(bodies, rr.Body.String())
	}
	for _, body := range bodies[1:] {
		if body != bodies[0] {
			t.Fatalf("rejection bodies differ, which leaks which schedules exist: %q vs %q", bodies[0], body)
		}
	}
}

// TestScheduleWebhookRejectsDisabledSchedule pins that parking a schedule stops
// its webhook. The caller has proven they hold the secret, so unlike the
// rejections above this one says what actually happened.
func TestScheduleWebhookRejectsDisabledSchedule(t *testing.T) {
	_, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()
	secret := webhookTestSchedule(t, sched, "on-push")

	off := false
	if _, err := sched.Update(context.Background(), "on-push", schedule.UpdateParams{Enabled: &off}); err != nil {
		t.Fatalf("park schedule: %v", err)
	}

	rr := postWebhook(t, srv, "/api/schedules/on-push/webhook?secret="+secret, "{}")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body=%s)", rr.Code, rr.Body.String())
	}
}

func TestScheduleWebhookRejectsNonPost(t *testing.T) {
	_, srv, sched, cleanup := newGoalSchedulerTestServer(t)
	defer cleanup()
	secret := webhookTestSchedule(t, sched, "on-push")

	req := httptest.NewRequest(http.MethodGet, "/api/schedules/on-push/webhook?secret="+secret, nil)
	rr := httptest.NewRecorder()
	srv.handleSchedule(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
}
