package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// manageTools returns the full Podiom self-management tool set. Every handler
// forwards to an existing daemon REST endpoint via the manageClient, so the tools
// inherit the API's validation, persistence, and live-apply behavior. Tools are
// grouped by domain: the calling session itself, roadmap tasks, projects,
// schedules, skills, MCP servers, goals, agents, then config / logs / usage.
//
// sessionID/agentName are the calling session's identity (the --session/--agent
// flags Podiom injects per session). Goal tools stamp them into request bodies,
// and the tools that create durable work stamp them as authorship — both
// server-side of the model, so provenance never depends on the model remembering
// to pass its own identity.
func manageTools(c *manageClient, sessionID, agentName string) []mcpTool {
	var tools []mcpTool
	tools = append(tools, sessionTools(c, sessionID)...)
	tools = append(tools, taskTools(c, sessionID, agentName)...)
	tools = append(tools, projectTools(c)...)
	tools = append(tools, scheduleTools(c, sessionID, agentName)...)
	tools = append(tools, skillTools(c)...)
	tools = append(tools, mcpServerTools(c)...)
	tools = append(tools, goalTools(c, sessionID, agentName)...)
	tools = append(tools, agentTools(c, agentName)...)
	tools = append(tools, platformTools(c)...)
	return tools
}

// --- schema + argument helpers ---------------------------------------------

func objectSchema(required []string, props map[string]any) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func strProp(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}
func boolProp(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}
func intProp(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}
func strArrProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// envVarArrProp describes an array of {name, value} env var objects. value is
// optional: omit it to pass the var through from the daemon's own OS
// environment instead of storing a value in Podiom.
func envVarArrProp(desc string) map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]any{
				"name":  strProp("Env var name, e.g. UNIFI_NETWORK_PASSWORD."),
				"value": strProp("Value to store for this server. Omit to pass the var through from the daemon's own environment instead."),
			},
		},
		"description": desc,
	}
}

// confirmProp is the guard shared by every destructive tool.
var confirmProp = boolProp("Must be true to proceed. Only pass true after the user has explicitly asked for this destructive action.")

// argMap unmarshals a tools/call arguments object into a raw key map. An empty
// arguments payload yields an empty map rather than an error.
func argMap(args json.RawMessage) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}
	trimmed := strings.TrimSpace(string(args))
	if trimmed == "" || trimmed == "null" {
		return out, nil
	}
	if err := json.Unmarshal(args, &out); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return out, nil
}

func argString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func argBool(m map[string]json.RawMessage, key string) bool {
	raw, ok := m[key]
	if !ok {
		return false
	}
	var b bool
	_ = json.Unmarshal(raw, &b)
	return b
}

// requireField returns an error if the named argument is missing or blank.
func requireField(m map[string]json.RawMessage, key string) error {
	if strings.TrimSpace(argString(m, key)) == "" {
		return fmt.Errorf("%q is required", key)
	}
	return nil
}

// requireConfirm gates a destructive tool: it must be called with confirm=true.
func requireConfirm(m map[string]json.RawMessage) error {
	if !argBool(m, "confirm") {
		return fmt.Errorf("destructive action refused: pass confirm=true only after the user has explicitly agreed")
	}
	return nil
}

// bodyFrom builds a request body from the provided keys, keeping only those the
// caller actually supplied. Absent keys stay absent so the server's pointer-based
// PATCH semantics leave unspecified fields untouched.
func bodyFrom(m map[string]json.RawMessage, keys ...string) map[string]json.RawMessage {
	body := map[string]json.RawMessage{}
	for _, k := range keys {
		if raw, ok := m[k]; ok {
			body[k] = raw
		}
	}
	return body
}

// stampCreator records the calling session and agent as the author of a new
// durable artifact, so a schedule or task an agent decided to create is
// traceable back to the conversation it came out of. The identity comes from
// this process's own --session/--agent flags, never from the model, so it can be
// neither forged nor forgotten.
//
// Only creation is stamped. Updates deliberately leave the fields alone:
// authorship is a creation fact, and rewriting it on every edit would let a
// later agent claim a task the user made. The known gap is that
// podiom_update_task can set pickup_at on a user-created task and make it
// self-firing without becoming its author; closing that needs a per-field
// mutation log, which is out of proportion to the problem.
//
// The keys are deliberately distinct from the goal tools' session_id/agent_name:
// a task body already has assigned_agent and a schedule body already has agent,
// both meaning something else.
func stampCreator(body map[string]json.RawMessage, sessionID, agentName string) map[string]json.RawMessage {
	if body == nil {
		body = map[string]json.RawMessage{}
	}
	if strings.TrimSpace(sessionID) != "" {
		body["created_by_session"], _ = json.Marshal(sessionID)
	}
	if strings.TrimSpace(agentName) != "" {
		body["created_by_agent"], _ = json.Marshal(agentName)
	}
	return body
}

// --- roadmap tasks ----------------------------------------------------------

func taskTools(c *manageClient, sessionID, agentName string) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_list_tasks",
			APIRoutes:   []string{"/api/tasks"},
			Description: "List roadmap items (tasks). Optionally filter by project_id, status (backlog|in_progress|review|done), or assigned_agent.",
			InputSchema: objectSchema(nil, map[string]any{
				"project_id":     strProp("Only return tasks in this project."),
				"status":         strProp("Only return tasks with this status: backlog, in_progress, review, or done."),
				"assigned_agent": strProp("Only return tasks assigned to this agent."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				body, err := c.get(ctx, "/api/tasks")
				if err != nil {
					return "", err
				}
				return filterTasks(body, argString(m, "project_id"), argString(m, "status"), argString(m, "assigned_agent"))
			},
		},
		{
			Name:        "podiom_get_task",
			APIRoutes:   []string{"/api/tasks/"},
			Description: "Get a single roadmap item (task) by id.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": strProp("Task id.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				return c.get(ctx, "/api/tasks/"+url.PathEscape(argString(m, "id")))
			},
		},
		{
			Name:        "podiom_create_task",
			APIRoutes:   []string{"/api/tasks"},
			Description: "Create a roadmap item (task). plan_required=true puts the agent in plan mode for this task. Leave pickup_at empty for an on-demand task, or set an RFC3339 timestamp to schedule automatic pickup. The task records your session and agent name, and the user sees it as created by you with a link back to this conversation." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"title"}, map[string]any{
				"project_id":     strProp("Project id this task belongs to."),
				"title":          strProp("Short task title."),
				"body":           strProp("Task description / prompt."),
				"assigned_agent": strProp("Agent that will work the task."),
				"provider":       strProp("Provider override: claude or codex. Omit for agent default."),
				"profile":        strProp("Profile override. Omit for agent default."),
				"model":          strProp("Model override. Required with any run target override."),
				"effort":         strProp("Effort override. Required with any run target override."),
				"status":         strProp("Initial status (defaults to backlog): backlog, in_progress, review, done."),
				"plan_required":  boolProp("Require plan mode for this task."),
				"pickup_at":      strProp("RFC3339 time to auto-start the task; empty = on-demand."),
				"goal_id":        strProp("Goal id when this task is part of a goal's plan. REQUIRED for tasks you create for a goal: it links the task's runs to the goal timeline and runs them autonomously (yolo)."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "title"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "project_id", "title", "body", "assigned_agent", "provider", "profile", "model", "effort", "status", "plan_required", "pickup_at", "goal_id")
				return c.post(ctx, "/api/tasks", stampCreator(body, sessionID, agentName))
			},
		},
		{
			Name:        "podiom_update_task",
			APIRoutes:   []string{"/api/tasks/"},
			Description: "Update fields of an existing roadmap item (task). Only the fields you pass are changed." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":             strProp("Task id."),
				"project_id":     strProp("New project id."),
				"title":          strProp("New title."),
				"body":           strProp("New description / prompt."),
				"assigned_agent": strProp("New assignee agent."),
				"provider":       strProp("Provider override: claude or codex. Empty string returns to agent default."),
				"profile":        strProp("Profile override. Empty string returns to agent default."),
				"model":          strProp("Model override. Empty string returns to agent default only when the whole target is empty."),
				"effort":         strProp("Effort override. Empty string returns to agent default only when the whole target is empty."),
				"status":         strProp("New status: backlog, in_progress, review, done. Setting in_progress does not start the task or create a session — use podiom_start_task for that."),
				"plan_required":  boolProp("Toggle plan mode."),
				"pickup_at":      strProp("RFC3339 scheduled pickup time; empty string clears it."),
				"goal_id":        strProp("Goal id to link this task to a goal (its runs become autonomous and audited on the goal timeline); empty string unlinks it."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "project_id", "title", "body", "assigned_agent", "provider", "profile", "model", "effort", "status", "plan_required", "pickup_at", "goal_id")
				return c.patch(ctx, "/api/tasks/"+url.PathEscape(argString(m, "id")), body)
			},
		},
		{
			Name:        "podiom_delete_task",
			APIRoutes:   []string{"/api/tasks/"},
			Description: "Delete a roadmap item (task). Destructive: requires confirm=true.",
			InputSchema: objectSchema([]string{"id", "confirm"}, map[string]any{
				"id":      strProp("Task id."),
				"confirm": confirmProp,
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				if err := requireConfirm(m); err != nil {
					return "", err
				}
				return c.del(ctx, "/api/tasks/"+url.PathEscape(argString(m, "id")))
			},
		},
		{
			Name:        "podiom_start_task",
			APIRoutes:   []string{"/api/tasks/"},
			Description: "Start a roadmap item (task) now: creates a session for its assigned agent, moves it to in_progress, and immediately runs the task as that agent's first turn. Returns as soon as the session exists; the run continues in the background. A task linked to a goal (goal_id) runs autonomously; a task with no goal_id runs with all side effects denied.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": strProp("Task id.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				// Always unattended: an MCP caller has no browser to send the first
				// turn, so without this the session would sit empty and never run.
				return c.post(ctx, "/api/tasks/"+url.PathEscape(argString(m, "id"))+"/start", map[string]any{"unattended": true})
			},
		},
	}
}

// filterTasks applies optional client-side filters to the /api/tasks response,
// which the endpoint returns unfiltered.
func filterTasks(body, projectID, status, assignedAgent string) (string, error) {
	if projectID == "" && status == "" && assignedAgent == "" {
		return body, nil
	}
	var tasks []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &tasks); err != nil {
		return body, nil // not an array we understand; return as-is
	}
	kept := make([]map[string]json.RawMessage, 0, len(tasks))
	for _, t := range tasks {
		// The /api/tasks endpoint serializes store.Task with Go's default
		// marshaling (no JSON tags), so the response keys are PascalCase.
		if projectID != "" && argString(t, "ProjectID") != projectID {
			continue
		}
		if status != "" && argString(t, "Status") != status {
			continue
		}
		if assignedAgent != "" && argString(t, "AssignedAgent") != assignedAgent {
			continue
		}
		kept = append(kept, t)
	}
	out, err := json.MarshalIndent(kept, "", "  ")
	if err != nil {
		return body, nil
	}
	return string(out), nil
}

// --- projects ---------------------------------------------------------------

func projectTools(c *manageClient) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_list_projects",
			APIRoutes:   []string{"/api/projects"},
			Description: "List all projects.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/projects")
			},
		},
		{
			Name:        "podiom_get_project",
			APIRoutes:   []string{"/api/projects/"},
			Description: "Get a single project by id.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": strProp("Project id.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				return c.get(ctx, "/api/projects/"+url.PathEscape(argString(m, "id")))
			},
		},
		{
			Name:        "podiom_create_project",
			APIRoutes:   []string{"/api/projects"},
			Description: "Create a project. id is optional (derived from name when omitted)." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"id":          strProp("Explicit project id (optional)."),
				"name":        strProp("Project name."),
				"description": strProp("Project description."),
				"stack":       strArrProp("Technology stack tags."),
				"notes":       strProp("Freeform notes."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "id", "name", "description", "stack", "notes")
				return c.post(ctx, "/api/projects", body)
			},
		},
		{
			Name:        "podiom_update_project",
			APIRoutes:   []string{"/api/projects/"},
			Description: "Update fields of a project. Only the fields you pass are changed. To remove a project use podiom_delete_project; to keep it but hide it, set status=archived." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":          strProp("Project id."),
				"name":        strProp("New name."),
				"description": strProp("New description."),
				"color":       strProp("New color."),
				"status":      strProp("New status (e.g. archived)."),
				"stack":       strArrProp("New stack tags."),
				"notes":       strProp("New notes."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "name", "description", "color", "status", "stack", "notes")
				return c.patch(ctx, "/api/projects/"+url.PathEscape(argString(m, "id")), body)
			},
		},
		{
			Name:      "podiom_delete_project",
			APIRoutes: []string{"/api/projects/"},
			Description: "Delete a project. Destructive: requires confirm=true. The project's files on disk are kept, " +
				"and its tasks and sessions are not deleted — they are orphaned (kept without a project).",
			InputSchema: objectSchema([]string{"id", "confirm"}, map[string]any{
				"id":      strProp("Project id."),
				"confirm": confirmProp,
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				if err := requireConfirm(m); err != nil {
					return "", err
				}
				return c.del(ctx, "/api/projects/"+url.PathEscape(argString(m, "id")))
			},
		},
	}
}

// --- schedules --------------------------------------------------------------

func scheduleTools(c *manageClient, sessionID, agentName string) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_list_schedules",
			APIRoutes:   []string{"/api/schedules"},
			Description: "List all schedules with their next run time and recent run history.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/schedules")
			},
		},
		{
			Name:        "podiom_create_schedule",
			APIRoutes:   []string{"/api/schedules"},
			Description: "Create a schedule: a recurring job that starts unattended sessions on its own from then on. Provide exactly one of cron (5-field) or every (interval like 6h); use podiom_update_schedule to change it later, including enabled=false to park it without losing its history. Create one when the user asked for work that repeats, and tell them the name you used so they can find it. The schedule records your session and agent name, and the user sees it as created by you with a link back to this conversation." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"name", "agent", "body"}, map[string]any{
				"name":           strProp("Schedule name (also the filename)."),
				"agent":          strProp("Agent that runs the schedule."),
				"body":           strProp("The task prompt the agent runs each time."),
				"cron":           strProp("5-field cron spec (mutually exclusive with every)."),
				"every":          strProp("Interval like 6h or 30m (mutually exclusive with cron)."),
				"provider":       strProp("Provider override: claude or codex. Omit for agent default."),
				"profile":        strProp("Profile override. Omit for agent default."),
				"model":          strProp("Model override (optional)."),
				"effort":         strProp("Effort override (optional)."),
				"run_permission": strProp("preapproved (default) or yolo."),
				"allowed_tools":  strArrProp("Tools permitted under preapproved runs."),
				"goal_id":        strProp("Goal id, if this schedule is part of a goal's plan (optional). Pass the id from your goal brief so this schedule shows up as linked to the goal."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				for _, f := range []string{"name", "agent", "body"} {
					if err := requireField(m, f); err != nil {
						return "", err
					}
				}
				body := bodyFrom(m, "name", "agent", "body", "cron", "every", "provider", "profile", "model", "effort", "run_permission", "allowed_tools", "goal_id")
				return c.post(ctx, "/api/schedules", stampCreator(body, sessionID, agentName))
			},
		},
		{
			Name:        "podiom_get_schedule",
			APIRoutes:   []string{"/api/schedules/"},
			Description: "Get one schedule in full: the agent that runs it, its cadence, run permission and allowed tools, whether it is enabled, the task body it runs each time, its next run time, who created it, and its recent runs with any errors. Read this before podiom_update_schedule so you patch real current values rather than assumptions.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{"name": strProp("Schedule name.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				return c.get(ctx, "/api/schedules/"+url.PathEscape(argString(m, "name")))
			},
		},
		{
			Name:      "podiom_update_schedule",
			APIRoutes: []string{"/api/schedules/"},
			Description: "Update an existing schedule in place. Only the fields you pass are changed; everything else in the file is preserved, including its run history and who created it. " +
				"Set enabled=false to park a schedule — it stays on disk with its history but stops firing automatically (you can still trigger it with podiom_run_schedule); enabled=true re-arms it. " +
				"Setting cron clears every, and setting every clears cron. The name cannot be changed — delete and recreate to rename. A schedule's goal link cannot be changed here, because a goal-linked schedule runs with full autonomy." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name":           strProp("Schedule name."),
				"agent":          strProp("New agent to run the schedule."),
				"body":           strProp("New task prompt."),
				"cron":           strProp("New 5-field cron spec (clears every)."),
				"every":          strProp("New interval like 6h or 30m (clears cron)."),
				"provider":       strProp("Provider override: claude or codex."),
				"profile":        strProp("Profile override."),
				"model":          strProp("Model override."),
				"effort":         strProp("Effort override."),
				"run_permission": strProp("preapproved or yolo."),
				"allowed_tools":  strArrProp("Tools permitted under preapproved runs (replaces the current list)."),
				"enabled":        boolProp("false parks the schedule without deleting it; true re-arms it."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "agent", "body", "cron", "every", "provider", "profile", "model", "effort", "run_permission", "allowed_tools", "enabled")
				if len(body) == 0 {
					return "", fmt.Errorf("provide at least one field to change")
				}
				return c.patch(ctx, "/api/schedules/"+url.PathEscape(argString(m, "name")), body)
			},
		},
		{
			Name:        "podiom_delete_schedule",
			APIRoutes:   []string{"/api/schedules/"},
			Description: "Delete a schedule. Destructive: requires confirm=true.",
			InputSchema: objectSchema([]string{"name", "confirm"}, map[string]any{
				"name":    strProp("Schedule name."),
				"confirm": confirmProp,
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				if err := requireConfirm(m); err != nil {
					return "", err
				}
				return c.del(ctx, "/api/schedules/"+url.PathEscape(argString(m, "name")))
			},
		},
		{
			Name:        "podiom_run_schedule",
			APIRoutes:   []string{"/api/schedules/"},
			Description: "Run a schedule immediately, outside its normal cadence.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{"name": strProp("Schedule name.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				return c.post(ctx, "/api/schedules/"+url.PathEscape(argString(m, "name"))+"/run", nil)
			},
		},
	}
}

// --- skills (marketplace) ---------------------------------------------------

func skillTools(c *manageClient) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_search_skills",
			APIRoutes:   []string{"/api/skills/search"},
			Description: "Search the skills marketplace.",
			InputSchema: objectSchema(nil, map[string]any{
				"query":    strProp("Search text."),
				"registry": strProp("Restrict to a registry."),
				"sort":     strProp("Sort order."),
				"page":     intProp("Page number (0-based)."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				q := url.Values{}
				if v := argString(m, "query"); v != "" {
					q.Set("q", v)
				}
				if v := argString(m, "registry"); v != "" {
					q.Set("registry", v)
				}
				if v := argString(m, "sort"); v != "" {
					q.Set("sort", v)
				}
				if raw, ok := m["page"]; ok {
					var p int
					if json.Unmarshal(raw, &p) == nil {
						q.Set("page", strconv.Itoa(p))
					}
				}
				return c.get(ctx, "/api/skills/search?"+q.Encode())
			},
		},
		{
			Name:        "podiom_list_installed_skills",
			APIRoutes:   []string{"/api/skills/installed"},
			Description: "List installed marketplace skills.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/skills/installed")
			},
		},
		{
			Name:        "podiom_install_skill",
			APIRoutes:   []string{"/api/skills/install"},
			Description: "Install a skill from the marketplace. Provide either registry+id, or a direct url. Set acknowledge=true to install a skill that contains executable content.",
			InputSchema: objectSchema(nil, map[string]any{
				"registry":    strProp("Registry name (use with id)."),
				"id":          strProp("Skill id within the registry (use with registry)."),
				"url":         strProp("Direct URL to a skill (alternative to registry+id)."),
				"acknowledge": boolProp("Acknowledge and allow executable skill content."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				body := bodyFrom(m, "registry", "id", "url", "acknowledge")
				return c.post(ctx, "/api/skills/install", body)
			},
		},
		{
			Name:        "podiom_uninstall_skill",
			APIRoutes:   []string{"/api/skills/installed/"},
			Description: "Uninstall a marketplace skill. Destructive: requires confirm=true.",
			InputSchema: objectSchema([]string{"name", "confirm"}, map[string]any{
				"name":    strProp("Installed skill name."),
				"confirm": confirmProp,
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				if err := requireConfirm(m); err != nil {
					return "", err
				}
				return c.del(ctx, "/api/skills/installed/"+url.PathEscape(argString(m, "name")))
			},
		},
	}
}

// --- MCP servers ------------------------------------------------------------

func mcpServerTools(c *manageClient) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_list_mcp",
			APIRoutes:   []string{"/api/mcp"},
			Description: "List MCP servers in the Podiom catalogue along with agents and their assignments.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/mcp")
			},
		},
		{
			Name:        "podiom_add_mcp_server",
			APIRoutes:   []string{"/api/mcp/servers"},
			Description: "Add or update an MCP server in the Podiom catalogue. Use transport=http with url, or transport=stdio with command (+args, +env_vars).",
			InputSchema: objectSchema([]string{"name", "transport"}, map[string]any{
				"name":      strProp("Server name."),
				"transport": strProp("http or stdio."),
				"url":       strProp("HTTP endpoint (for transport=http)."),
				"command":   strProp("Command to launch (for transport=stdio)."),
				"args":      strArrProp("Command arguments (for transport=stdio)."),
				"env_vars":  envVarArrProp("Environment variables for the launched process, e.g. [{\"name\":\"UNIFI_NETWORK_PASSWORD\",\"value\":\"...\"}]."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				if err := requireField(m, "transport"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "name", "transport", "url", "command", "args", "env_vars")
				return c.post(ctx, "/api/mcp/servers", body)
			},
		},
		{
			Name:        "podiom_remove_mcp_server",
			APIRoutes:   []string{"/api/mcp/servers/"},
			Description: "Remove an MCP server from the Podiom catalogue. Destructive: requires confirm=true. Only Podiom-managed servers can be removed.",
			InputSchema: objectSchema([]string{"name", "confirm"}, map[string]any{
				"name":    strProp("Server name."),
				"confirm": confirmProp,
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				if err := requireConfirm(m); err != nil {
					return "", err
				}
				return c.del(ctx, "/api/mcp/servers/"+url.PathEscape(argString(m, "name")))
			},
		},
		{
			Name:        "podiom_test_mcp_server",
			APIRoutes:   []string{"/api/mcp/servers/"},
			Description: "Test an MCP server: connect and perform a JSON-RPC handshake, returning whether it works and its tool count.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{"name": strProp("Server name.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				return c.post(ctx, "/api/mcp/servers/"+url.PathEscape(argString(m, "name"))+"/test", nil)
			},
		},
		{
			Name:        "podiom_assign_mcp_server",
			APIRoutes:   []string{"/api/mcp/assignments"},
			Description: "Assign or unassign an MCP server to an agent. Set assigned=false to unassign.",
			InputSchema: objectSchema([]string{"agent_name", "server_name", "assigned"}, map[string]any{
				"agent_name":  strProp("Agent to change."),
				"server_name": strProp("MCP server name."),
				"assigned":    boolProp("true to assign, false to unassign."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "agent_name"); err != nil {
					return "", err
				}
				if err := requireField(m, "server_name"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "agent_name", "server_name", "assigned")
				return c.put(ctx, "/api/mcp/assignments", body)
			},
		},
	}
}

// --- config / logs / usage ---------------------------------------------------

func platformTools(c *manageClient) []mcpTool {
	return []mcpTool{
		{
			Name:        "podiom_get_config",
			APIRoutes:   []string{"/api/config"},
			Description: "Get the global Podiom configuration (default provider, profile, model, effort, permission mode, timeout, fallback).",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/config")
			},
		},
		{
			Name:        "podiom_patch_config",
			APIRoutes:   []string{"/api/config"},
			Description: "Update global Podiom configuration. Only the fields you pass are changed; changes are validated, persisted, and applied live without a restart.",
			InputSchema: objectSchema(nil, map[string]any{
				"provider":           strProp("Default provider (claude|codex)."),
				"profile":            strProp("Default auth profile."),
				"model":              strProp("Default model."),
				"effort":             strProp("Default effort."),
				"permission_mode":    strProp("approve, auto, or yolo."),
				"permission_timeout": strProp("Permission prompt timeout (e.g. 3m)."),
				"fallback":           strArrProp("Ordered fallback provider list."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				body := bodyFrom(m, "provider", "profile", "model", "effort", "permission_mode", "permission_timeout", "fallback")
				if len(body) == 0 {
					return "", fmt.Errorf("provide at least one config field to change")
				}
				return c.patch(ctx, "/api/config", body)
			},
		},
		{
			Name:      "podiom_get_usage",
			APIRoutes: []string{"/api/usage"},
			Description: "Read how much of each provider plan's rate-limit windows is already spent, per auth profile: the windows with their used percentage and reset time, the plan name, and any extra-credit balance. " +
				"Check it before fanning work out — starting several tasks or a tight schedule when the weekly window is nearly spent just moves the failure later. " +
				"The figures are the provider's own and refresh in the background, so a profile may report a credentials or rate-limit error instead of windows. For this session's own token use, call podiom_session_context.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				// No refresh option on purpose: the tracker already refreshes on an
				// interval, and ?refresh=1 forces live provider calls that a model
				// would happily trigger in a loop.
				return c.get(ctx, "/api/usage")
			},
		},
		{
			Name:        "podiom_read_logs",
			APIRoutes:   []string{"/api/logs"},
			Description: "Read the tail of the Podiom daemon log. Pass lines to control how many (default 200, max 5000). Call again for newer lines.",
			InputSchema: objectSchema(nil, map[string]any{
				"lines": intProp("Number of trailing log lines to return (default 200, max 5000)."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				path := "/api/logs"
				if raw, ok := m["lines"]; ok {
					var n int
					if json.Unmarshal(raw, &n) == nil && n > 0 {
						path += "?lines=" + strconv.Itoa(n)
					}
				}
				return c.get(ctx, path)
			},
		},
	}
}
