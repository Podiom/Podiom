package schedule

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSchedule(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write schedule: %v", err)
	}
	return path
}

func TestParseValidCronSchedule(t *testing.T) {
	dir := t.TempDir()
	path := writeSchedule(t, dir, "morning.md", `---
agent: jared
provider: codex
profile: work
model: sonnet
effort: low
cron: "0 7 * * *"
run_permission: preapproved
allowed_tools: [Read]
enabled: true
---

Summarise today's calendar.
`)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sched.Name != "morning" || sched.Agent != "jared" {
		t.Fatalf("unexpected schedule: %+v", sched)
	}
	if sched.CronSpec() != "0 7 * * *" {
		t.Fatalf("cron spec = %q", sched.CronSpec())
	}
	if sched.Provider != "codex" || sched.Profile != "work" {
		t.Fatalf("target = %s/%s", sched.Provider, sched.Profile)
	}
	if sched.Body != "Summarise today's calendar." {
		t.Fatalf("body = %q", sched.Body)
	}
	if !sched.Enabled || sched.RunPermission != PermissionPreapproved {
		t.Fatalf("flags wrong: %+v", sched)
	}
}

func TestRenderIncludesRunTarget(t *testing.T) {
	text := Render(CreateParams{
		Name:     "nightly",
		Agent:    "jared",
		Provider: "codex",
		Profile:  "work",
		Model:    "gpt-5.1",
		Effort:   "high",
		Cron:     "0 1 * * *",
		Body:     "Run the audit.",
	})
	for _, want := range []string{"provider: codex", "profile: work", "model: gpt-5.1", "effort: high"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered schedule missing %q:\n%s", want, text)
		}
	}
}

// A schedule's project decides which workspace its runs get, so it has to
// survive the Render -> Parse round trip every write goes through, and stay
// absent (rather than becoming "") on a file that names no project.
func TestProjectRoundTrip(t *testing.T) {
	text := Render(CreateParams{
		Name:    "nightly",
		Agent:   "jared",
		Cron:    "0 1 * * *",
		Project: "mission-control",
		Body:    "Run the audit.",
	})
	if !strings.Contains(text, "project: mission-control") {
		t.Fatalf("rendered schedule missing the project:\n%s", text)
	}
	sched, err := Parse(writeSchedule(t, t.TempDir(), "nightly.md", text))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sched.Project != "mission-control" {
		t.Fatalf("project did not survive the round trip: %+v", sched)
	}

	unbound, err := Parse(writeSchedule(t, t.TempDir(), "plain.md", Render(CreateParams{
		Name:  "plain",
		Agent: "jared",
		Cron:  "0 1 * * *",
		Body:  "Run the audit.",
	})))
	if err != nil {
		t.Fatalf("parse unbound: %v", err)
	}
	if unbound.Project != "" {
		t.Fatalf("schedule with no project should stay unbound, got %q", unbound.Project)
	}
}

// TestCreatorProvenanceRoundTrip pins that a schedule an agent authored records
// which session it came from, and survives the Render -> Parse round trip that
// every write goes through.
func TestCreatorProvenanceRoundTrip(t *testing.T) {
	text := Render(CreateParams{
		Name:             "nightly",
		Agent:            "jared",
		Cron:             "0 1 * * *",
		CreatedBySession: "sess-1",
		CreatedByAgent:   "jared",
		Body:             "Run the audit.",
	})
	for _, want := range []string{"created_by_session: sess-1", "created_by_agent: jared"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered schedule missing %q:\n%s", want, text)
		}
	}

	path := writeSchedule(t, t.TempDir(), "nightly.md", text)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sched.CreatedBySession != "sess-1" || sched.CreatedByAgent != "jared" {
		t.Fatalf("provenance did not survive the round trip: %+v", sched)
	}

	// A human-authored file carries no attribution rather than a wrong one.
	plain := writeSchedule(t, t.TempDir(), "manual.md", `---
agent: jared
cron: "0 1 * * *"
enabled: true
---
Run the audit.
`)
	manual, err := Parse(plain)
	if err != nil {
		t.Fatalf("parse manual: %v", err)
	}
	if manual.CreatedBySession != "" || manual.CreatedByAgent != "" {
		t.Fatalf("human-authored schedule should carry no attribution: %+v", manual)
	}
}

func TestParseEveryMapsToDescriptor(t *testing.T) {
	dir := t.TempDir()
	path := writeSchedule(t, dir, "freq.md", `---
agent: jared
every: 6h
enabled: true
---
Do a thing.
`)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if sched.CronSpec() != "@every 6h" {
		t.Fatalf("every spec = %q", sched.CronSpec())
	}
	// run_permission defaults to the stricter preapproved.
	if sched.RunPermission != PermissionPreapproved {
		t.Fatalf("default permission = %q", sched.RunPermission)
	}
}

func TestParseValidatesCronSpecs(t *testing.T) {
	dir := t.TempDir()
	invalid := []string{"not a cron", "99 99 * * *", "* * * *", "@bogus"}
	for _, spec := range invalid {
		t.Run("rejects "+spec, func(t *testing.T) {
			path := writeSchedule(t, dir, Slug("bad-"+spec)+".md", "---\nagent: jared\ncron: \""+spec+"\"\nrun_permission: preapproved\nenabled: true\n---\nbody\n")
			if _, err := Parse(path); err == nil {
				t.Fatalf("Parse accepted invalid cron spec %q", spec)
			}
		})
	}

	valid := []string{"0 3 * * *", "*/15 * * * *", "@daily", "@every 1h"}
	for _, spec := range valid {
		t.Run("accepts "+spec, func(t *testing.T) {
			path := writeSchedule(t, dir, Slug("good-"+spec)+".md", "---\nagent: jared\ncron: \""+spec+"\"\nrun_permission: preapproved\nenabled: true\n---\nbody\n")
			sched, err := Parse(path)
			if err != nil {
				t.Fatalf("Parse rejected valid cron spec %q: %v", spec, err)
			}
			if sched.CronSpec() != spec {
				t.Fatalf("CronSpec() = %q, want %q", sched.CronSpec(), spec)
			}
		})
	}
}

// TestParseWebhookOnlySchedule pins that a webhook is a trigger in its own
// right: a schedule with no cadence at all is valid, and it registers no cron
// entry because it has no spec to register.
func TestParseWebhookOnlySchedule(t *testing.T) {
	path := writeSchedule(t, t.TempDir(), "on-push.md", `---
agent: jared
webhook: true
webhook_secret: s3cr3t
enabled: true
---
React to the push.
`)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !sched.Webhook || sched.WebhookSecret != "s3cr3t" {
		t.Fatalf("webhook fields wrong: %+v", sched)
	}
	if sched.CronSpec() != "" {
		t.Fatalf("webhook-only schedule should have no cron spec, got %q", sched.CronSpec())
	}
}

// TestParseWebhookAlongsideCron pins that the two triggers are additive rather
// than exclusive: a schedule can fire on a clock and on an external call.
func TestParseWebhookAlongsideCron(t *testing.T) {
	path := writeSchedule(t, t.TempDir(), "both.md", `---
agent: jared
cron: "0 7 * * *"
webhook: true
webhook_secret: s3cr3t
enabled: true
---
Do the thing.
`)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !sched.Webhook || sched.CronSpec() != "0 7 * * *" {
		t.Fatalf("expected both triggers, got %+v", sched)
	}
}

// TestRenderWebhookRoundTrip pins the file format for a webhook-only schedule:
// both keys are written, and no empty cron line is emitted for a schedule that
// has no cadence — that line would fail to parse back as a valid trigger.
func TestRenderWebhookRoundTrip(t *testing.T) {
	text := Render(CreateParams{
		Name:          "on-push",
		Agent:         "jared",
		Webhook:       true,
		WebhookSecret: "s3cr3t",
		Enabled:       true,
		Body:          "React to the push.",
	})
	for _, want := range []string{"webhook: true", "webhook_secret: s3cr3t"} {
		if !strings.Contains(text, want) {
			t.Fatalf("rendered schedule missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "cron:") {
		t.Fatalf("webhook-only schedule should not render a cron line:\n%s", text)
	}

	path := writeSchedule(t, t.TempDir(), "on-push.md", text)
	sched, err := Parse(path)
	if err != nil {
		t.Fatalf("parse rendered webhook schedule: %v", err)
	}
	if !sched.Webhook || sched.WebhookSecret != "s3cr3t" {
		t.Fatalf("webhook did not survive the round trip: %+v", sched)
	}
}

func TestParseRejectsInvalidSchedules(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"no-front.md":    "just a body, no frontmatter\n",
		"no-agent.md":    "---\ncron: \"0 7 * * *\"\nenabled: true\n---\nbody\n",
		"no-timing.md":   "---\nagent: jared\nenabled: true\n---\nbody\n",
		"both-timing.md": "---\nagent: jared\ncron: \"0 7 * * *\"\nevery: 6h\nenabled: true\n---\nbody\n",
		"bad-perm.md":    "---\nagent: jared\ncron: \"0 7 * * *\"\nrun_permission: sometimes\nenabled: true\n---\nbody\n",
		"empty-body.md":  "---\nagent: jared\ncron: \"0 7 * * *\"\nenabled: true\n---\n",
		// A webhook with no secret would be an open trigger, so it must never parse.
		"hook-no-secret.md": "---\nagent: jared\nwebhook: true\nenabled: true\n---\nbody\n",
	}
	for name, content := range cases {
		path := writeSchedule(t, dir, name, content)
		if _, err := Parse(path); err == nil {
			t.Errorf("expected parse error for %s, got nil", name)
		}
	}
}

func TestScanDirSeparatesValidAndInvalid(t *testing.T) {
	dir := t.TempDir()
	writeSchedule(t, dir, "good.md", "---\nagent: jared\nevery: 1h\nenabled: true\n---\nwork\n")
	writeSchedule(t, dir, "bad.md", "---\nagent: jared\n---\nno timing\n")
	writeSchedule(t, dir, "notes.txt", "ignored")

	schedules, parseErrs, err := ScanDir(dir)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(schedules) != 1 || schedules[0].Name != "good" {
		t.Fatalf("unexpected valid schedules: %+v", schedules)
	}
	if _, ok := parseErrs["bad"]; !ok {
		t.Fatalf("expected parse error for bad, got %+v", parseErrs)
	}
}

func TestScanDirMissingDirIsEmpty(t *testing.T) {
	schedules, parseErrs, err := ScanDir(filepath.Join(t.TempDir(), "does-not-exist"))
	if err != nil {
		t.Fatalf("scan missing dir: %v", err)
	}
	if len(schedules) != 0 || len(parseErrs) != 0 {
		t.Fatalf("expected empty results, got %d schedules / %d errs", len(schedules), len(parseErrs))
	}
}
