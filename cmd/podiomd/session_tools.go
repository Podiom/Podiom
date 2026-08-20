package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
)

const workspaceFileProseGuidance = " If that prose needs to share material from a workspace text file, first call podiom_attach_workspace_file and include its returned markdown_link; never refer the user to a local path."

// sessionTools is the agent's view of its own session. The session id comes from
// this process's own --session flag, so an agent can only ever describe or
// update the session it is running in.
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
		{
			Name:      "podiom_update_session_project",
			APIRoutes: []string{"/api/session-context/"},
			Description: "Set or clear the project override for the session you are running in. " +
				"Pass an existing project id to use that project's workspace and instructions from the next turn. " +
				"Pass an empty project_id to clear the override: an ordinary session becomes unassigned, while a task, schedule, or goal session returns to its inherited project. " +
				"The current turn stays in its existing workspace. The session is resolved from your own process, so you cannot change anyone else's.",
			InputSchema: objectSchema([]string{"project_id"}, map[string]any{
				"project_id": strProp("Existing project id to select, or an empty string to clear the override."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				if sessionID == "" {
					return "", fmt.Errorf("no session id: this tool is only available inside a Podiom session")
				}
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if _, ok := m["project_id"]; !ok {
					return "", fmt.Errorf("%q is required", "project_id")
				}
				return c.patch(ctx, "/api/session-context/"+url.PathEscape(sessionID), bodyFrom(m, "project_id"))
			},
		},
		{
			Name:      "podiom_attach_workspace_file",
			APIRoutes: []string{"/api/workspace-files"},
			Description: "Snapshot a UTF-8 text file from your current workspace and get a durable Markdown link to show the user. " +
				"Use this whenever the user needs to read, copy, review, or act on file content: never tell them to browse the local filesystem. " +
				"The path must be relative to your current project work root (or your agent workspace when this session has no project). " +
				"The returned snapshot does not change if the source file is edited or deleted. Put the returned markdown_link in your reply or in any Podiom prose field, with enough surrounding context to explain it.",
			InputSchema: objectSchema([]string{"path"}, map[string]any{
				"path":  strProp("Path relative to the current work root."),
				"label": strProp("Optional user-facing link label; defaults to the filename."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				if sessionID == "" {
					return "", fmt.Errorf("no session id: this tool is only available inside a Podiom session")
				}
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "path"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "path", "label")
				body["session_id"], _ = json.Marshal(sessionID)
				return c.post(ctx, "/api/workspace-files", body)
			},
		},
	}
}
