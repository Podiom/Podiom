package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

// sessionTools is the agent's view of its own session. The session id comes from
// this process's own --session flag, so an agent can only ever describe the
// session it is running in.
//
// Deliberately absent, which is why /api/sessions stays on the notManageable
// list even though this tool exists:
//   - Listing other sessions. Another agent's conversation with the user is
//     theirs, and no management task needs it.
//   - Creating a session. That is spawning an unattended run of a colleague;
//     podiom_start_task and podiom_run_schedule are the sanctioned, audited ways
//     to cause work to happen.
//   - Deleting a session. That destroys the user's own conversation history.
func sessionTools(c *manageClient, sessionID string) []mcpTool {
	return []mcpTool{
		{
			Name:      "podiom_session_context",
			APIRoutes: []string{"/api/session-context/"},
			Description: "Describe the session you are running in. Returns who you are; why this session exists (origin: web, cli, schedule, roadmap, goal) and whether it is unattended — " +
				"a schedule, roadmap, or goal run has nobody watching, so a question asked in your reply reaches no one and you should use podiom_ask_user instead; " +
				"what it is linked to (project, roadmap task, goal, schedule and run id); the roadmap tasks and schedules you have created in this session; " +
				"the provider, model, and permission mode you are running under; and how much of the context window you have used. " +
				"Takes no arguments — the session is resolved from your own process, so you cannot ask about anyone else's. " +
				"Reach for it when the answer would change what you do: whether anyone is watching, which task or goal you were started for, or whether you have room left for more work.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				if sessionID == "" {
					return "", fmt.Errorf("no session id: this tool is only available inside a Podiom session")
				}
				return c.get(ctx, "/api/session-context/"+url.PathEscape(sessionID))
			},
		},
	}
}
