// Package schedule is Podiom's embedded scheduler. A schedule is a single
// self-describing markdown file under ~/.podiom/schedules/<name>.md: YAML
// frontmatter declares the job, the markdown body is the task the named agent is
// prompted with (R7.2 / D23). The files are the source of truth — there is no
// schedules block in config.yaml.
package schedule

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/config"
	"gopkg.in/yaml.v3"
)

// RunPermission is a schedule's unattended permission policy (§7.7).
type RunPermission string

const (
	// PermissionPreapproved runs in approve mode with an allow-list; anything not
	// listed is auto-denied. This is the stricter default (R7.8).
	PermissionPreapproved RunPermission = "preapproved"
	// PermissionYolo runs with whole-machine auto-approval (deliberate opt-in).
	PermissionYolo RunPermission = "yolo"
)

// Schedule is one parsed schedule file.
type Schedule struct {
	Name          string          // file name without the .md extension
	Path          string          // absolute path to the source file
	Agent         string          // agent that runs the task (required)
	Provider      config.Provider // optional provider override
	Profile       string          // optional profile override
	Model         string          // optional model override
	Effort        string          // optional effort override
	Cron          string          // 5-field cron expression (mutually exclusive with Every)
	Every         string          // interval like "6h" (mutually exclusive with Cron)
	Webhook       bool            // additional trigger: an external POST can fire this schedule
	WebhookSecret string          // per-schedule secret the webhook caller must present
	RunPermission RunPermission   // preapproved (default) | yolo
	AllowedTools  []string        // preapproved allow-list
	Enabled       bool            // off switch: a disabled file stays but does not fire
	GoalID        string          // optional id of the goal whose plan created this schedule
	// Project binds the runs to a project: they work in its directory and receive
	// its standing instructions. Optional. A schedule created for a goal inherits
	// the goal's project unless it names its own.
	Project string
	// CreatedBySession and CreatedByAgent record the agent session that authored
	// this file, so a recurring job an agent decided to create is traceable back
	// to the conversation it came out of. Both empty means a human wrote it (the
	// UI, the CLI, or by dropping a file in the directory).
	CreatedBySession string
	CreatedByAgent   string
	Body             string // the task prompt
}

// frontmatter mirrors the YAML block at the top of a schedule file.
type frontmatter struct {
	Agent            string   `yaml:"agent"`
	Provider         string   `yaml:"provider"`
	Profile          string   `yaml:"profile"`
	Model            string   `yaml:"model"`
	Effort           string   `yaml:"effort"`
	Cron             string   `yaml:"cron"`
	Every            string   `yaml:"every"`
	Webhook          bool     `yaml:"webhook"`
	WebhookSecret    string   `yaml:"webhook_secret"`
	RunPermission    string   `yaml:"run_permission"`
	AllowedTools     []string `yaml:"allowed_tools"`
	Enabled          bool     `yaml:"enabled"`
	GoalID           string   `yaml:"goal_id"`
	Project          string   `yaml:"project"`
	CreatedBySession string   `yaml:"created_by_session"`
	CreatedByAgent   string   `yaml:"created_by_agent"`
}

// CronSpec returns the robfig/cron spec for this schedule. `every: 6h` maps to
// the "@every 6h" descriptor; a cron expression is used verbatim. A
// webhook-only schedule has no cadence and returns "", which is what tells the
// scheduler not to register a cron entry for it.
func (s Schedule) CronSpec() string {
	if s.Every != "" {
		return "@every " + s.Every
	}
	return s.Cron
}

// newWebhookSecret mints the per-schedule secret a webhook caller must present.
// 32 bytes of entropy, base64url without padding — the same shape as the
// gateway token, but deliberately its own value: a leaked webhook secret must
// only be able to start the one job that owns it.
func newWebhookSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate webhook secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// Parse reads and validates a single schedule file.
func Parse(path string) (Schedule, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Schedule{}, err
	}
	return parseBytes(path, raw)
}

// parseBytes validates schedule content already in memory. `path` is only used
// to derive the schedule name and error context.
func parseBytes(path string, raw []byte) (Schedule, error) {
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return Schedule{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}

	var meta frontmatter
	if err := yaml.Unmarshal(fm, &meta); err != nil {
		return Schedule{}, fmt.Errorf("%s: parse frontmatter: %w", filepath.Base(path), err)
	}

	name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	sched := Schedule{
		Name:          name,
		Path:          path,
		Agent:         strings.TrimSpace(meta.Agent),
		Provider:      config.Provider(strings.TrimSpace(meta.Provider)),
		Profile:       strings.TrimSpace(meta.Profile),
		Model:         strings.TrimSpace(meta.Model),
		Effort:        strings.TrimSpace(meta.Effort),
		Cron:          strings.TrimSpace(meta.Cron),
		Every:         strings.TrimSpace(meta.Every),
		Webhook:       meta.Webhook,
		WebhookSecret: strings.TrimSpace(meta.WebhookSecret),
		RunPermission: RunPermission(strings.TrimSpace(meta.RunPermission)),
		AllowedTools:  meta.AllowedTools,
		Enabled:       meta.Enabled,
		GoalID:        strings.TrimSpace(meta.GoalID),
		Project:       strings.TrimSpace(meta.Project),

		CreatedBySession: strings.TrimSpace(meta.CreatedBySession),
		CreatedByAgent:   strings.TrimSpace(meta.CreatedByAgent),
		Body:             strings.TrimSpace(string(body)),
	}
	if sched.RunPermission == "" {
		sched.RunPermission = PermissionPreapproved
	}
	if err := sched.validate(); err != nil {
		return Schedule{}, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return sched, nil
}

func (s Schedule) validate() error {
	if s.Agent == "" {
		return fmt.Errorf("agent is required")
	}
	if s.Provider != "" && !config.KnownProvider(s.Provider) {
		return fmt.Errorf("invalid provider %q (want %s)", s.Provider, config.ProviderIDsLabel())
	}
	if s.Cron == "" && s.Every == "" && !s.Webhook {
		return fmt.Errorf("a cron, every, or webhook trigger is required")
	}
	if s.Cron != "" && s.Every != "" {
		return fmt.Errorf("set only one of cron or every, not both")
	}
	if s.Every != "" {
		if _, err := time.ParseDuration(s.Every); err != nil {
			return fmt.Errorf("invalid every %q: %w", s.Every, err)
		}
	}
	// A webhook without a secret would be an open trigger. Parsing must never
	// yield one, so the endpoint can rely on the secret always being present.
	if s.Webhook && s.WebhookSecret == "" {
		return fmt.Errorf("webhook_secret is required when webhook is true")
	}
	switch s.RunPermission {
	case PermissionPreapproved, PermissionYolo:
	default:
		return fmt.Errorf("invalid run_permission %q (want preapproved|yolo)", s.RunPermission)
	}
	if s.Body == "" {
		return fmt.Errorf("task body is empty")
	}
	return nil
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

// Slug normalizes a schedule name into a safe file stem (lowercase, dashes).
func Slug(name string) string {
	s := slugStrip.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	return strings.Trim(s, "-")
}

// Render produces the markdown file content (frontmatter + body) for a new
// schedule. Empty optional fields are omitted from the frontmatter.
func Render(p CreateParams) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("agent: " + p.Agent + "\n")
	if p.GoalID != "" {
		b.WriteString("goal_id: " + p.GoalID + "\n")
	}
	if p.Project != "" {
		b.WriteString("project: " + p.Project + "\n")
	}
	if p.CreatedBySession != "" {
		b.WriteString("created_by_session: " + p.CreatedBySession + "\n")
	}
	if p.CreatedByAgent != "" {
		b.WriteString("created_by_agent: " + p.CreatedByAgent + "\n")
	}
	if p.Provider != "" {
		b.WriteString("provider: " + string(p.Provider) + "\n")
	}
	if p.Profile != "" {
		b.WriteString("profile: " + p.Profile + "\n")
	}
	if p.Model != "" {
		b.WriteString("model: " + p.Model + "\n")
	}
	if p.Effort != "" {
		b.WriteString("effort: " + p.Effort + "\n")
	}
	if p.Every != "" {
		b.WriteString("every: " + p.Every + "\n")
	} else if p.Cron != "" {
		b.WriteString("cron: " + p.Cron + "\n")
	}
	if p.Webhook {
		b.WriteString("webhook: true\n")
		b.WriteString("webhook_secret: " + p.WebhookSecret + "\n")
	}
	perm := p.RunPermission
	if perm == "" {
		perm = PermissionPreapproved
	}
	b.WriteString("run_permission: " + string(perm) + "\n")
	if len(p.AllowedTools) > 0 {
		b.WriteString("allowed_tools:\n")
		for _, t := range p.AllowedTools {
			b.WriteString("  - " + t + "\n")
		}
	}
	b.WriteString("enabled: " + strconv.FormatBool(p.Enabled) + "\n")
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(p.Body))
	b.WriteString("\n")
	return b.String()
}

// splitFrontmatter separates a leading `---` delimited YAML block from the body.
// A file without frontmatter is an error: a schedule needs its frontmatter to be
// self-sufficient (R7.3).
func splitFrontmatter(raw []byte) (front, body []byte, err error) {
	text := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return nil, nil, fmt.Errorf("missing YAML frontmatter (file must start with ---)")
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, nil, fmt.Errorf("unterminated YAML frontmatter (missing closing ---)")
	}
	front = []byte(rest[:end])
	after := rest[end+len("\n---"):]
	if i := strings.IndexByte(after, '\n'); i >= 0 {
		after = after[i+1:]
	} else {
		after = ""
	}
	return front, []byte(after), nil
}

// ScanDir parses every *.md file in dir, returning successful schedules and a
// map of filename -> parse error for the rest, so callers can surface invalid
// files without failing the whole scan. A missing directory yields empty results.
func ScanDir(dir string) ([]Schedule, map[string]error, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, map[string]error{}, nil
		}
		return nil, nil, err
	}
	var schedules []Schedule
	parseErrs := map[string]error{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.EqualFold(filepath.Ext(entry.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		sched, err := Parse(path)
		if err != nil {
			name := strings.TrimSuffix(entry.Name(), filepath.Ext(entry.Name()))
			parseErrs[name] = err
			continue
		}
		schedules = append(schedules, sched)
	}
	return schedules, parseErrs, nil
}
