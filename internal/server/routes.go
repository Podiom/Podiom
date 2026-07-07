package server

import (
	"net/http"
	"strings"
)

// apiRoute pairs a mux pattern with its handler. Collecting the /api surface in
// one table (rather than inline mux.HandleFunc calls) makes it enumerable: the
// manage-mcp coverage guardrail test asserts every route here is either exposed
// as an agent tool or explicitly excluded, so the API and the tool set can't
// drift apart silently as Podiom grows.
type apiRoute struct {
	pattern string
	handler http.HandlerFunc
}

// apiRoutes is the single source of truth for the daemon's /api surface. New()
// registers each entry on the mux; APIRoutePatterns() reads the patterns for the
// coverage test. Non-/api routes (/healthz, the conditional /terminal/, and the
// / SPA handler) stay inline in New() and are intentionally out of this table.
func (s *Server) apiRoutes() []apiRoute {
	return []apiRoute{
		{"/api/auth/check", s.handleAuthCheck},
		{"/api/token/rotate", s.handleTokenRotate},
		{"/api/onboarding", s.handleOnboardingState},
		{"/api/onboarding/complete", s.handleOnboardingComplete},
		{"/api/onboarding/token", s.handleOnboardingToken},
		{"/api/agents", s.handleAgents},
		{"/api/agents/", s.handleAgent},
		{"/api/memory/status", s.handleMemoryStatus},
		{"/api/profiles", s.handleProfiles},
		{"/api/profiles/", s.handleProfile},
		{"/api/sessions", s.handleSessions},
		{"/api/sessions/", s.handleSession},
		{"/api/plans/", s.handlePlan},
		{"/api/chat", s.handleChat},
		{"/api/ws", s.handleWebSocket},
		{"/api/schedules", s.handleSchedules},
		{"/api/schedules/", s.handleSchedule},
		{"/api/projects", s.handleProjects},
		{"/api/projects/", s.handleProject},
		{"/api/github/status", s.handleGitHubStatus},
		{"/api/github/device/start", s.handleGitHubDeviceStart},
		{"/api/github/device/poll", s.handleGitHubDevicePoll},
		{"/api/github/repos", s.handleGitHubRepos},
		{"/api/tasks", s.handleTasks},
		{"/api/tasks/", s.handleTask},
		{"/api/goals", s.handleGoals},
		{"/api/goals/", s.handleGoal},
		{"/api/access-requests", s.handleAccessRequests},
		{"/api/access-requests/", s.handleAccessRequest},
		{"/api/skills", s.handleSkills},
		{"/api/skills/relink", s.handleSkillsRelink},
		// Skill marketplace (Spec 07). Registered so the more specific
		// /api/skills/... patterns win over the /api/skills catalogue route.
		{"/api/skills/search", s.handleSkillSearch},
		{"/api/skills/detail", s.handleSkillDetail},
		{"/api/skills/detail/file", s.handleSkillFile},
		{"/api/skills/resolve", s.handleSkillResolveURL},
		{"/api/skills/install", s.handleSkillInstall},
		{"/api/skills/installed", s.handleSkillsInstalled},
		{"/api/skills/installed/", s.handleSkillInstalledItem},
		{"/api/mcp", s.handleMCP},
		{"/api/mcp/servers", s.handleMCPServers},
		{"/api/mcp/servers/", s.handleMCPServer},
		{"/api/mcp/assignments", s.handleMCPAssignments},
		{"/api/logs", s.handleLogs},
		{"/api/logs/follow", s.handleLogsFollow},
		{"/api/permission-decisions/", s.handlePermissionDecision},
		{"/api/user-input-decisions/", s.handleUserInputDecision},
		{"/api/permissions/", s.handlePermissionRequest},
		{"/api/update", s.handleUpdate},
		{"/api/update/apply", s.handleUpdateApply},
		{"/api/config", s.handleConfig},
		{"/api/provider-capabilities", s.handleProviderCapabilities},
		{"/api/usage", s.handleUsage},
		{"/api/push/vapid", s.handlePushVAPID},
		{"/api/push/subscribe", s.handlePushSubscribe},
		{"/api/push/unsubscribe", s.handlePushUnsubscribe},
	}
}

// APIRoutePatterns lists the registered /api route patterns. It only reads the
// static patterns, so a zero-value Server is sufficient — the handlers are bound
// as method values but never invoked here — letting the coverage test call it
// without wiring up dependencies.
func APIRoutePatterns() []string {
	s := &Server{}
	out := make([]string, 0)
	for _, rt := range s.apiRoutes() {
		if strings.HasPrefix(rt.pattern, "/api/") {
			out = append(out, rt.pattern)
		}
	}
	return out
}
