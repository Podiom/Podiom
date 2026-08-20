package main

import (
	"sort"
	"testing"

	"github.com/Podiom/Podiom/internal/server"
)

// notManageable is the explicit allow-list of /api routes intentionally NOT
// exposed as manage-mcp agent tools. Every entry needs a reason. Adding a new
// manageable route without a tool makes TestManageToolsCoverAPIRoutes fail until
// the route is either wrapped by a podiom_* tool or listed here — that forced
// choice is the whole point: it stops the API surface and the agent tool set
// from drifting apart silently as Podiom grows.
var notManageable = map[string]string{
	"/api/auth/check":                "auth plumbing, not a management op",
	"/api/token/rotate":              "security-sensitive, human-only",
	"/api/onboarding":                "first-run onboarding flow",
	"/api/onboarding/complete":       "first-run onboarding flow",
	"/api/onboarding/token":          "first-run onboarding flow",
	"/api/memory/status":             "memory subsystem status, read via UI",
	"/api/profiles":                  "auth profiles hold credentials; human-managed",
	"/api/profiles/":                 "auth profiles hold credentials; human-managed",
	"/api/provider-status":           "provider login state, read via the Settings UI",
	"/api/provider-login":            "signing in to an account is human-only; an agent must never authenticate on the user's behalf",
	"/api/provider-login/":           "signing in to an account is human-only; an agent must never authenticate on the user's behalf",
	"/api/sessions":                  "creating a session spawns an unattended run of a colleague (podiom_start_task / podiom_run_schedule are the audited ways to cause work) and listing every session exposes the user's private chats with other agents",
	"/api/sessions/":                 "deleting a session destroys the user's own conversation history, and the full detail replays the whole transcript; an agent reads and updates the safe subset of its own session via /api/session-context/",
	"/api/attachments/":              "browser photo transport, human-only",
	"/api/workspace-files/":          "immutable snapshot content is read by the authenticated dashboard; agents create links through podiom_attach_workspace_file",
	"/api/plans/":                    "plan submission handled by plan-mcp, not manage-mcp",
	"/api/session-project/":          "session-scoped project context handled by project-mcp, not manage-mcp",
	"/api/git/status":                "host git setup; the agent reads readiness via podiom_project_context",
	"/api/git/identity":              "writes the user's own git identity; human-only by design",
	"/api/chat":                      "chat transport, not a management op",
	"/api/ws":                        "websocket transport",
	"/api/github/status":             "github integration, UI-driven",
	"/api/github/device/start":       "github device auth, UI-driven",
	"/api/github/device/poll":        "github device auth, UI-driven",
	"/api/github/repos":              "github repo listing, UI-driven",
	"/api/skills":                    "local skills catalogue read; marketplace tools cover install/search",
	"/api/skills/relink":             "internal skills-union maintenance",
	"/api/skills/detail":             "pre-install inspection view, UI-driven",
	"/api/skills/detail/file":        "pre-install file viewer, UI-driven",
	"/api/skills/resolve":            "monorepo URL resolution used by the install UI",
	"/api/logs/follow":               "never-terminating stream; podiom_read_logs polls instead",
	"/api/permission-decisions/":     "permission relay plumbing",
	"/api/user-input-requests/":      "user-input relay plumbing",
	"/api/user-input-decisions/":     "user-input relay plumbing",
	"/api/interviews/":               "USER.md interview relay plumbing",
	"/api/permissions/":              "permission relay plumbing",
	"/api/update":                    "self-update, HA/human-managed",
	"/api/update/apply":              "self-update, HA/human-managed",
	"/api/provider-capabilities":     "static provider metadata",
	"/api/transcribe":                "browser voice-input transport, human-only",
	"/api/notifications":             "the user's own notification inbox; an agent must not read what the user has been told",
	"/api/notifications/":            "reading and resolving the user's notifications is human-only",
	"/api/notifications/read-all":    "reading the user's notifications is human-only",
	"/api/notifications/preferences": "notification preferences are the user's own settings",
	"/api/notification-devices":      "native push transport",
	"/api/notification-devices/test": "buzzes the user's own phone; human-only by design",
	"/api/notification-devices/":     "native push transport",
	"/api/push/vapid":                "web push transport",
	"/api/push/subscribe":            "web push transport",
	"/api/push/unsubscribe":          "web push transport",
	"/api/access-requests/":          "approve/deny decisions are human-only; an agent must never grant its own request",
	"/api/goal-rate-limits/":         "rate-limit recovery changes goal run targets; human-only decision",
	"/api/agent-questions/":          "answering a deferred agent question is human-only; the agent asks via podiom_ask_user",
	"/api/goal-action-items/":        "responding to an action item is human-only; the agent hands work over via podiom_request_user_action",
	"/api/user-profile":              "USER.md is human-authored via the interview UI; agents receive it as context",
	"/api/credentials/":              "deleting a stored secret is human-only; agents store one with podiom_store_credential and read the listing with podiom_list_credentials",
}

// coveredRoutes is the union of every route the manage tools declare they hit.
func coveredRoutes() map[string]bool {
	covered := map[string]bool{}
	for _, tl := range manageTools(newManageClient("127.0.0.1:8787"), "", "") {
		for _, r := range tl.APIRoutes {
			covered[r] = true
		}
	}
	return covered
}

// TestManageToolsCoverAPIRoutes fails when a registered /api route is neither
// wrapped by a manage tool nor explicitly excluded. This is the guardrail: it
// converts silent tool drift into a build failure at PR time.
func TestManageToolsCoverAPIRoutes(t *testing.T) {
	covered := coveredRoutes()
	var missing []string
	for _, pattern := range server.APIRoutePatterns() {
		if covered[pattern] {
			continue
		}
		if _, ok := notManageable[pattern]; ok {
			continue
		}
		missing = append(missing, pattern)
	}
	sort.Strings(missing)
	for _, pattern := range missing {
		t.Errorf("API route %q has no manage-mcp tool. Add a podiom_* tool that sets APIRoutes to %q, or add it to notManageable with a reason.", pattern, pattern)
	}
}

// TestManageToolRoutesExist checks the reverse: every route a tool claims to
// exercise must be a real registered route. Catches typos and routes deleted or
// renamed out from under a tool.
func TestManageToolRoutesExist(t *testing.T) {
	real := map[string]bool{}
	for _, pattern := range server.APIRoutePatterns() {
		real[pattern] = true
	}
	for _, tl := range manageTools(newManageClient("127.0.0.1:8787"), "", "") {
		if len(tl.APIRoutes) == 0 {
			t.Errorf("tool %q declares no APIRoutes; every management tool must map to at least one route", tl.Name)
			continue
		}
		for _, r := range tl.APIRoutes {
			if !real[r] {
				t.Errorf("tool %q declares APIRoutes %q which is not a registered server route", tl.Name, r)
			}
		}
	}
}

// TestNotManageableEntriesExist keeps the exclusion list honest: every excluded
// route must still be a real registered route, so a removed or renamed route
// can't leave a stale exclusion silently masking a real gap.
func TestNotManageableEntriesExist(t *testing.T) {
	real := map[string]bool{}
	for _, pattern := range server.APIRoutePatterns() {
		real[pattern] = true
	}
	for pattern := range notManageable {
		if !real[pattern] {
			t.Errorf("notManageable lists %q which is no longer a registered server route; remove the stale exclusion", pattern)
		}
	}
}
