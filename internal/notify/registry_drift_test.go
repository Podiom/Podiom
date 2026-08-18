package notify

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Podiom/Podiom/internal/store"
)

// specifiedButNotProduced lists notification types the requirements name but
// Podiom deliberately does not produce, each with the reason and the preference
// row the requirements sketch for it. Keeping the gap here rather than in a
// comment means the requirements and the registry are provably in sync, and a
// type that later grows a real producer cannot be forgotten — moving it into the
// registry is what makes this test pass again.
var specifiedButNotProduced = map[string]struct {
	Label  string
	Reason string
}{
	"system.execution_failed": {
		Label:  "Important failures",
		Reason: "Podiom has no system-failure concept distinct from a turn or run failure, so nothing would emit it; system.warning covers the daemon-level case",
	},
}

// labelsNotInPreferenceSketch lists registry labels the requirements' preference
// UI sketch omits. The sketch is explicitly approximate ("the exact wording MAY
// evolve"), so a produced type it forgot is a documentation gap, not a bug — but
// it is recorded rather than ignored so the sketch can be corrected.
var labelsNotInPreferenceSketch = map[string]string{
	TypeTaskFailed: "the R6 sketch's Tasks group omits a failure row, though R3 lists task.failed and R11 asks for it",
}

// TestRegistryMatchesRequirementsDoc keeps the registry and the notification
// types listed in the requirements from drifting apart. Adding a type to the doc
// without producing it (or the reverse) fails here.
func TestRegistryMatchesRequirementsDoc(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(root, "docs", "requirements", "notifications.md"))
	if err != nil {
		t.Fatalf("read requirements: %v", err)
	}

	documented := parseRequirementTypes(t, string(doc))
	if len(documented) == 0 {
		t.Fatal("no notification types parsed from the requirements doc; has the R3 section moved?")
	}

	registered := map[string]bool{}
	for _, info := range All() {
		registered[info.Type] = true
	}

	for notifType := range documented {
		if registered[notifType] {
			continue
		}
		if gap, ok := specifiedButNotProduced[notifType]; ok {
			if gap.Reason == "" {
				t.Errorf("specifiedButNotProduced[%q] needs a reason", notifType)
			}
			continue
		}
		t.Errorf("notification type %q is in the requirements but not in the registry; "+
			"add it to the registry or record why it has no producer in specifiedButNotProduced",
			notifType)
	}
	for notifType := range registered {
		if !documented[notifType] {
			t.Errorf("notification type %q is in the registry but not in the requirements doc", notifType)
		}
	}
	for notifType := range specifiedButNotProduced {
		if registered[notifType] {
			t.Errorf("notification type %q is now produced; remove it from specifiedButNotProduced", notifType)
		}
		if !documented[notifType] {
			t.Errorf("notification type %q is in specifiedButNotProduced but no longer in the requirements doc", notifType)
		}
	}
}

// parseRequirementTypes pulls the notification types out of the fenced block
// under "## R3. Notification types".
func parseRequirementTypes(t *testing.T, doc string) map[string]bool {
	t.Helper()
	_, rest, ok := strings.Cut(doc, "## R3. Notification types")
	if !ok {
		t.Fatal(`requirements doc has no "## R3. Notification types" heading`)
	}
	_, rest, ok = strings.Cut(rest, "```")
	if !ok {
		t.Fatal("R3 section has no fenced block")
	}
	block, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatal("R3 fenced block is not closed")
	}
	typePattern := regexp.MustCompile(`^(session|schedule|goal|task|system)\.[a-z_]+$`)
	out := map[string]bool{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if typePattern.MatchString(line) {
			out[line] = true
		}
	}
	return out
}

// TestNotificationTypeLiteralsStayInRegistry keeps notification type strings out
// of producers and handlers: they must reference the exported Type* constants so
// the registry stays the only place that knows what types exist.
//
// If this fails on your change, use the constant rather than adding an allowlist
// entry. Only genuinely new sanctioned locations belong in `allowed`.
func TestNotificationTypeLiteralsStayInRegistry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	allowed := []string{
		"internal/notify/", // the registry and the engine that reads it
	}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "dist": true, "bin": true,
		"docs": true, "ha": true, "scripts": true, "testdata": true, "web": true,
	}

	// Quoted, fully-qualified type literals ("goal.action_requested"). Prose and
	// dotted identifiers do not match.
	literal := regexp.MustCompile("[\"'`](session|schedule|goal|task|system)\\.[a-z_]+[\"'`]")

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		for _, prefix := range allowed {
			if strings.HasPrefix(rel, prefix) {
				return nil
			}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(body), "\n") {
			if match := literal.FindString(line); match != "" {
				t.Errorf("%s:%d uses notification type literal %s; reference the notify.Type* constant instead",
					rel, i+1, match)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRegistryInvariants checks the structural rules every entry must satisfy, so
// a half-filled entry fails at build time rather than producing a notification
// with no label or an action nothing can execute.
func TestRegistryInvariants(t *testing.T) {
	knownImportance := map[store.NotificationImportance]bool{
		store.NotificationPassive: true, store.NotificationNormal: true,
		store.NotificationImportant: true, store.NotificationCritical: true,
	}
	knownCategory := map[Category]bool{}
	for _, c := range Categories() {
		knownCategory[c] = true
	}
	knownAction := map[ActionID]bool{
		ActionOpen: true, ActionAllow: true, ActionDeny: true, ActionApprove: true,
		ActionDone: true, ActionBlocked: true, ActionReview: true, ActionMarkDone: true,
	}

	seen := map[string]bool{}
	for _, info := range All() {
		if seen[info.Type] {
			t.Errorf("duplicate registry entry for %q", info.Type)
		}
		seen[info.Type] = true

		if info.Type == "" || info.Label == "" || info.Producer == "" {
			t.Errorf("%q: Type, Label and Producer must all be set", info.Type)
		}
		if !knownCategory[info.Category] {
			t.Errorf("%q: unknown category %q", info.Type, info.Category)
		}
		if !knownImportance[info.Importance] {
			t.Errorf("%q: unknown importance %q", info.Type, info.Importance)
		}
		for _, id := range info.Actions {
			if !knownAction[id] {
				t.Errorf("%q: unknown action %q", info.Type, id)
			}
		}
		// Tapping a notification must always be able to open something, so any
		// type offering actions must offer a way in as well.
		if len(info.Actions) > 0 {
			hasEntry := false
			for _, id := range info.Actions {
				if id == ActionOpen || id == ActionReview {
					hasEntry = true
				}
			}
			if !hasEntry {
				t.Errorf("%q: offers actions but no open/review entry point", info.Type)
			}
			if info.NavTarget == "" {
				t.Errorf("%q: offers actions but has no navigation target", info.Type)
			}
		}
		// Anything the user can act on has to name the object being acted upon,
		// otherwise the action has no target and resolution has nothing to clear.
		if info.Actionable() && info.Resource == ResourceNone {
			t.Errorf("%q: is actionable but names no resource", info.Type)
		}
	}
}

// TestDefaultPreferencesMatchR7 pins the shipped defaults to the requirements:
// events that block progress notify out of the box, high-frequency informational
// ones do not.
func TestDefaultPreferencesMatchR7(t *testing.T) {
	tests := []struct {
		notifType string
		want      bool
	}{
		// Requires the user before anything can continue.
		{TypeSessionQuestion, true},
		{TypeSessionPermissionRequired, true},
		{TypeGoalActionRequested, true},
		{TypeGoalAccessRequested, true},
		{TypeGoalCompletionProposed, true},
		{TypeGoalRateLimited, true},
		{TypeGoalRunFailed, true},
		{TypeScheduleFailed, true},
		{TypeTaskReviewRequired, true},

		// Potentially high-frequency and purely informational.
		{TypeScheduleStarted, false},
		{TypeScheduleSucceeded, false},
		{TypeGoalRunStarted, false},
		{TypeGoalProgress, false},
		{TypeGoalMetricChanged, false},
		{TypeGoalPlanChanged, false},
	}
	for _, tc := range tests {
		t.Run(tc.notifType, func(t *testing.T) {
			info, ok := Lookup(tc.notifType)
			if !ok {
				t.Fatalf("%q is not registered", tc.notifType)
			}
			if info.DefaultOn != tc.want {
				t.Errorf("DefaultOn = %v, want %v", info.DefaultOn, tc.want)
			}
		})
	}
}

// TestImportanceMatchesR25 pins the importance examples the requirements give.
func TestImportanceMatchesR25(t *testing.T) {
	tests := []struct {
		notifType string
		want      store.NotificationImportance
	}{
		{TypeGoalMetricChanged, store.NotificationPassive},
		{TypeScheduleSucceeded, store.NotificationNormal},
		{TypeGoalActionRequested, store.NotificationImportant},
		{TypeSessionPermissionRequired, store.NotificationImportant},
		{TypeGoalRunFailed, store.NotificationImportant},
	}
	for _, tc := range tests {
		t.Run(tc.notifType, func(t *testing.T) {
			info, ok := Lookup(tc.notifType)
			if !ok {
				t.Fatalf("%q is not registered", tc.notifType)
			}
			if info.Importance != tc.want {
				t.Errorf("Importance = %q, want %q", info.Importance, tc.want)
			}
		})
	}
}

// TestCategoriesCoverR6UI checks every registry entry's category and label appear
// in the requirements' preference-UI sketch, and vice versa. This is what lets
// the preference screen be rendered from the server with no labels hardcoded in
// the client.
func TestCategoriesCoverR6UI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join(root, "docs", "requirements", "notifications.md"))
	if err != nil {
		t.Fatalf("read requirements: %v", err)
	}
	documented := parseRequirementUILabels(t, string(doc))

	for _, info := range All() {
		labels, ok := documented[info.Category]
		if !ok {
			t.Errorf("%q: category %q has no group in the R6 UI sketch", info.Type, info.Category)
			continue
		}
		if labels[info.Label] {
			continue
		}
		if reason, ok := labelsNotInPreferenceSketch[info.Type]; ok {
			if reason == "" {
				t.Errorf("labelsNotInPreferenceSketch[%q] needs a reason", info.Type)
			}
			continue
		}
		t.Errorf("%q: label %q is not in the R6 UI sketch under %q; add it to the sketch "+
			"or record the omission in labelsNotInPreferenceSketch", info.Type, info.Label, info.Category)
	}

	registered := map[Category]map[string]bool{}
	for _, info := range All() {
		if registered[info.Category] == nil {
			registered[info.Category] = map[string]bool{}
		}
		registered[info.Category][info.Label] = true
	}
	// Rows the sketch shows for types nothing produces are accounted for by
	// specifiedButNotProduced, not by a registry entry.
	unproduced := map[string]bool{}
	for _, gap := range specifiedButNotProduced {
		if gap.Label != "" {
			unproduced[gap.Label] = true
		}
	}
	for category, labels := range documented {
		for label := range labels {
			if registered[category][label] || unproduced[label] {
				continue
			}
			t.Errorf("R6 UI sketch lists %q under %q but no registry entry produces it", label, category)
		}
	}

	for notifType := range labelsNotInPreferenceSketch {
		if _, ok := Lookup(notifType); !ok {
			t.Errorf("labelsNotInPreferenceSketch names %q, which is not registered", notifType)
		}
	}
}

// parseRequirementUILabels reads the preference-UI sketch in R6 into
// category -> set of labels. Group headings in the sketch are prose ("Agent
// interaction"), so they are mapped onto the Category constants here.
func parseRequirementUILabels(t *testing.T, doc string) map[Category]map[string]bool {
	t.Helper()
	_, rest, ok := strings.Cut(doc, "## R6. Initial preference UI")
	if !ok {
		t.Fatal(`requirements doc has no "## R6. Initial preference UI" heading`)
	}
	_, rest, ok = strings.Cut(rest, "```")
	if !ok {
		t.Fatal("R6 section has no fenced block")
	}
	block, _, ok := strings.Cut(rest, "```")
	if !ok {
		t.Fatal("R6 fenced block is not closed")
	}

	headings := map[string]Category{
		"Agent interaction": CategoryAgent,
		"Goals":             CategoryGoals,
		"Schedules":         CategorySchedules,
		"Tasks":             CategoryTasks,
		"System":            CategorySystem,
	}
	out := map[Category]map[string]bool{}
	var current Category
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "Notifications" {
			continue
		}
		if category, ok := headings[line]; ok {
			current = category
			if out[current] == nil {
				out[current] = map[string]bool{}
			}
			continue
		}
		// Rows are "✓ Label" (default on) or "□ Label" (default off).
		label := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "✓"), "□"))
		if label == line || label == "" || current == "" {
			continue
		}
		out[current][label] = true
	}
	return out
}
