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
// grouped by domain: roadmap tasks, projects, schedules, skills, MCP servers,
// then config / logs / agents.
func manageTools(c *manageClient) []mcpTool {
	var tools []mcpTool
	tools = append(tools, taskTools(c)...)
	tools = append(tools, projectTools(c)...)
	tools = append(tools, scheduleTools(c)...)
	tools = append(tools, skillTools(c)...)
	tools = append(tools, mcpServerTools(c)...)
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

func strProp(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func boolProp(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func intProp(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func strArrProp(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
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

// --- roadmap tasks ----------------------------------------------------------

func taskTools(c *manageClient) []mcpTool {
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
			Description: "Create a roadmap item (task). plan_required=true puts the agent in plan mode for this task. Leave pickup_at empty for an on-demand task, or set an RFC3339 timestamp to schedule automatic pickup.",
			InputSchema: objectSchema([]string{"title"}, map[string]any{
				"project_id":     strProp("Project id this task belongs to."),
				"title":          strProp("Short task title."),
				"body":           strProp("Task description / prompt."),
				"assigned_agent": strProp("Agent that will work the task."),
				"status":         strProp("Initial status (defaults to backlog): backlog, in_progress, review, done."),
				"plan_required":  boolProp("Require plan mode for this task."),
				"pickup_at":      strProp("RFC3339 time to auto-start the task; empty = on-demand."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "title"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "project_id", "title", "body", "assigned_agent", "status", "plan_required", "pickup_at")
				return c.post(ctx, "/api/tasks", body)
			},
		},
		{
			Name:        "podiom_update_task",
			APIRoutes:   []string{"/api/tasks/"},
			Description: "Update fields of an existing roadmap item (task). Only the fields you pass are changed.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":             strProp("Task id."),
				"project_id":     strProp("New project id."),
				"title":          strProp("New title."),
				"body":           strProp("New description / prompt."),
				"assigned_agent": strProp("New assignee agent."),
				"status":         strProp("New status: backlog, in_progress, review, done."),
				"plan_required":  boolProp("Toggle plan mode."),
				"pickup_at":      strProp("RFC3339 scheduled pickup time; empty string clears it."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "project_id", "title", "body", "assigned_agent", "status", "plan_required", "pickup_at")
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
			Description: "Start a roadmap item (task) now: creates a session for its assigned agent and moves it to in_progress.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": strProp("Task id.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				return c.post(ctx, "/api/tasks/"+url.PathEscape(argString(m, "id"))+"/start", nil)
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
		if projectID != "" && argString(t, "project_id") != projectID {
			continue
		}
		if status != "" && argString(t, "status") != status {
			continue
		}
		if assignedAgent != "" && argString(t, "assigned_agent") != assignedAgent {
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
			Description: "Create a project. id is optional (derived from name when omitted).",
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
			Description: "Update fields of a project. Only the fields you pass are changed. To remove a project use podiom_delete_project; to keep it but hide it, set status=archived.",
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

func scheduleTools(c *manageClient) []mcpTool {
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
			Description: "Create a schedule. Provide exactly one of cron (5-field) or every (interval like 6h). There is no update tool; to change a schedule, delete and recreate it.",
			InputSchema: objectSchema([]string{"name", "agent", "body"}, map[string]any{
				"name":           strProp("Schedule name (also the filename)."),
				"agent":          strProp("Agent that runs the schedule."),
				"body":           strProp("The task prompt the agent runs each time."),
				"cron":           strProp("5-field cron spec (mutually exclusive with every)."),
				"every":          strProp("Interval like 6h or 30m (mutually exclusive with cron)."),
				"model":          strProp("Model override (optional)."),
				"effort":         strProp("Effort override (optional)."),
				"run_permission": strProp("preapproved (default) or yolo."),
				"allowed_tools":  strArrProp("Tools permitted under preapproved runs."),
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
				body := bodyFrom(m, "name", "agent", "body", "cron", "every", "model", "effort", "run_permission", "allowed_tools")
				return c.post(ctx, "/api/schedules", body)
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
				"env_vars":  strArrProp("Environment variable names to pass through."),
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

// --- config / logs / agents -------------------------------------------------

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
				"permission_mode":    strProp("approve or yolo."),
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
		{
			Name:        "podiom_list_agents",
			APIRoutes:   []string{"/api/agents"},
			Description: "List all agents and their properties.",
			InputSchema: objectSchema(nil, nil),
			Handler: func(ctx context.Context, _ json.RawMessage) (string, error) {
				return c.get(ctx, "/api/agents")
			},
		},
		{
			Name:        "podiom_get_agent",
			APIRoutes:   []string{"/api/agents/"},
			Description: "Get one agent's full detail, including its assigned MCP servers and its soul (identity prompt).",
			InputSchema: objectSchema([]string{"name"}, map[string]any{"name": strProp("Agent name.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				return c.get(ctx, "/api/agents/"+url.PathEscape(argString(m, "name")))
			},
		},
	}
}
