package main

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
)

// goalTools is the agent-facing surface of the Goals feature (goals spec §9):
// read goals, record progress and metric movement, adjust the plan-shaped
// fields, file access requests, and propose completion. Deliberately absent:
// creating or deleting goals (user-created), changing status or lead agent
// (user-only transitions), and deciding access requests (human-only — the
// /api/access-requests/ decision route has no tool on purpose).
//
// Every mutating call is stamped with the calling session/agent identity
// before it leaves the process, so timeline provenance never depends on the
// model remembering (or choosing) to identify itself.
func goalTools(c *manageClient, sessionID, agentName string) []mcpTool {
	// stamp injects the session/agent identity into a request body.
	stamp := func(body map[string]json.RawMessage) map[string]json.RawMessage {
		if body == nil {
			body = map[string]json.RawMessage{}
		}
		if strings.TrimSpace(sessionID) != "" {
			body["session_id"], _ = json.Marshal(sessionID)
		}
		if strings.TrimSpace(agentName) != "" {
			body["agent_name"], _ = json.Marshal(agentName)
		}
		return body
	}

	return []mcpTool{
		{
			Name:        "podiom_list_goals",
			APIRoutes:   []string{"/api/goals"},
			Description: "List goals. Optionally filter by status (active|paused|review|done|abandoned).",
			InputSchema: objectSchema(nil, map[string]any{
				"status": strProp("Only return goals with this status."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				path := "/api/goals"
				if status := strings.TrimSpace(argString(m, "status")); status != "" {
					path += "?status=" + url.QueryEscape(status)
				}
				return c.get(ctx, path)
			},
		},
		{
			Name:        "podiom_get_goal",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Get one goal with its recent timeline events and its access requests (including the user's decisions and notes).",
			InputSchema: objectSchema([]string{"id"}, map[string]any{"id": strProp("Goal id.")}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				return c.get(ctx, "/api/goals/"+url.PathEscape(argString(m, "id")))
			},
		},
		{
			Name:        "podiom_update_goal",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Update a goal's description, success criteria, metric definitions, or review cadence. Agents cannot change a goal's title, status, lead agent, or project." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":               strProp("Goal id."),
				"description":      strProp("New description."),
				"success_criteria": strProp("New success criteria (what \"done\" means)."),
				"metrics":          map[string]any{"type": "array", "description": "Full replacement metric list.", "items": map[string]any{"type": "object", "properties": map[string]any{"name": strProp("Metric name."), "target": map[string]any{"type": "number"}, "current": map[string]any{"type": "number"}, "unit": strProp("Optional unit.")}}},
				"review_every":     strProp("Review cadence as a duration, e.g. 24h (min 15m). Empty disables automatic reviews."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				body := stamp(bodyFrom(m, "description", "success_criteria", "metrics", "review_every"))
				return c.patch(ctx, "/api/goals/"+url.PathEscape(argString(m, "id")), body)
			},
		},
		{
			Name:        "podiom_record_goal_progress",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Record a goal timeline entry: what moved and the evidence (kind \"progress\"), or how the plan changed (kind \"plan_change\"). Pass metric_updates to move metric values — each update is audited old → new. Pass next_step to restate what you will do next; this is the one line the user reads to see where the goal is heading." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":   strProp("Goal id."),
				"kind": strProp("Entry kind: progress (default) or plan_change."),
				"body": strProp("Markdown describing what happened, with evidence."),
				"metric_updates": map[string]any{"type": "array", "description": "Metric values to move.", "items": map[string]any{
					"type":       "object",
					"required":   []string{"name", "current"},
					"properties": map[string]any{"name": strProp("Metric name (must exist on the goal)."), "current": map[string]any{"type": "number", "description": "New current value."}},
				}},
				"next_step":     strProp("One short imperative line naming the single most important strategic move you will make before the next review (e.g. \"Post the launch thread on r/selfhosted\"). Not a restatement of a task or schedule you created, and not a list. Omit to leave the current next step unchanged."),
				"next_step_why": strProp("One sentence on why that is the right move now."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				body := stamp(bodyFrom(m, "kind", "body", "metric_updates", "next_step", "next_step_why"))
				return c.post(ctx, "/api/goals/"+url.PathEscape(argString(m, "id"))+"/events", body)
			},
		},
		{
			Name:        "podiom_list_goal_events",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Page through a goal's timeline, newest first. Pass before (an event id) to load older entries. A user_feedback entry with Pinned true is a standing directive: binding for the goal's whole life, not a note about one moment. You cannot pin or unpin — only the user can.",
			InputSchema: objectSchema([]string{"id"}, map[string]any{
				"id":     strProp("Goal id."),
				"limit":  intProp("Max entries to return (default 50)."),
				"before": intProp("Only entries older than this event id."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				q := url.Values{}
				if v := strings.TrimSpace(argString(m, "limit")); v != "" && v != "0" {
					q.Set("limit", v)
				}
				if v := strings.TrimSpace(argString(m, "before")); v != "" && v != "0" {
					q.Set("before", v)
				}
				path := "/api/goals/" + url.PathEscape(argString(m, "id")) + "/events"
				if len(q) > 0 {
					path += "?" + q.Encode()
				}
				return c.get(ctx, path)
			},
		},
		{
			Name:        "podiom_commit_goal_memory",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Publish the complete durable working-memory update for the current goal planning or review run. Omitted state and items remain unchanged; retire an obsolete item explicitly with its id and a reason. A successful commit is required before the run can finish. Ordinary feedback stops replaying only when given a disposition that points at the memory items retaining its effect.",
			InputSchema: objectSchema([]string{"id", "base_revision", "outcome"}, map[string]any{
				"id":            strProp("Goal id."),
				"base_revision": map[string]any{"type": "integer", "description": "Memory revision supplied in the review packet (0 during initial planning)."},
				"current_state": strProp("Current goal state, at most 240 characters. Omit to preserve it."),
				"active_plan":   map[string]any{"type": "array", "description": "Full replacement active plan; each item is at most 180 characters.", "items": map[string]any{"type": "string"}},
				"upserts": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "required": []string{"id", "kind", "title"},
					"properties": map[string]any{
						"id": strProp("Stable item id."), "kind": strProp("milestone, decision, rejected, risk, or artifact."),
						"title": strProp("Short title."), "detail": strProp("Short detail."),
						"rationale": strProp("Short rationale."), "evidence": strProp("Short evidence."), "url": strProp("Artifact URL, if any."),
					},
				}},
				"retirements": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "required": []string{"id", "reason"},
					"properties": map[string]any{"id": strProp("Existing memory item id."), "reason": strProp("Why it is no longer active.")},
				}},
				"feedback_dispositions": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "required": []string{"event_id", "disposition"},
					"properties": map[string]any{
						"event_id":        intProp("Pending feedback event id."),
						"disposition":     strProp("incorporated, completed, or superseded."),
						"memory_item_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						"superseded_by":   intProp("Newer feedback event id when superseded."),
					},
				}},
				"outcome": strProp("What this run achieved, at most 240 characters."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				for _, field := range []string{"id", "base_revision", "outcome"} {
					if err := requireField(m, field); err != nil {
						return "", err
					}
				}
				body := stamp(bodyFrom(m, "base_revision", "current_state", "active_plan", "upserts", "retirements", "feedback_dispositions", "outcome"))
				return c.post(ctx, "/api/goals/"+url.PathEscape(argString(m, "id"))+"/memory", body)
			},
		},
		{
			Name:        "podiom_propose_goal_completion",
			APIRoutes:   []string{"/api/goals/"},
			Description: "Propose that a goal's success criteria are met. The goal enters review with your closing report and the user is notified — only the user can mark it done. The closing report should walk through each success criterion." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"id", "closing_report"}, map[string]any{
				"id":             strProp("Goal id."),
				"closing_report": strProp("Markdown closing report addressing every success criterion."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "id"); err != nil {
					return "", err
				}
				if err := requireField(m, "closing_report"); err != nil {
					return "", err
				}
				body := stamp(bodyFrom(m, "closing_report"))
				return c.post(ctx, "/api/goals/"+url.PathEscape(argString(m, "id"))+"/propose-completion", body)
			},
		},
		{
			Name:      "podiom_request_access",
			APIRoutes: []string{"/api/access-requests"},
			Description: "File an access request when you are missing a capability you genuinely cannot provision yourself. Goal runs already have full autonomous access (yolo), so you rarely need this: you can change files directly — do NOT request permission_mode for goal work, and do NOT request a CLI tool you could install yourself with podiom_install_tool. Reserve it for things Podiom must wire for you: assigning an MCP server, installing a marketplace skill, or an env var / credential. The user is notified and approves or denies; their decision (and note) reaches you at your next review. Kinds and payload fields: " +
				"mcp_server{server_name}, skill{registry,id,url}, cli_tool{tool}, env_var{var_name,purpose,target}, permission_mode{mode}. " +
				"cli_tool is only for a tool the USER must install host-wide (a system package, brew, apt) — approving it acknowledges the request and shows them the suggested command; Podiom installs nothing. " +
				"For anything you can install yourself, use podiom_install_tool instead: it needs no approval and puts the tool on every agent's PATH. install_hint is display-only context. " +
				"env_var requests a credential/API token by NAME (e.g. GITHUB_TOKEN for gh/GitHub API access when you are blocked on missing auth). " +
				"NEVER put the secret value in the request — name the variable and its purpose only; on approval the user enters the value privately in the Podiom UI and it is injected into your process environment for future runs. " +
				"A request shown as executed means the variable is already set: use it directly, and never print or echo its value." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"goal_id", "kind", "reason"}, map[string]any{
				"goal_id": strProp("Goal id this request unblocks."),
				"kind":    strProp("Request kind: mcp_server, skill, cli_tool, env_var, or permission_mode."),
				"payload": map[string]any{"type": "object", "description": "Kind-specific fields (see tool description).", "additionalProperties": map[string]any{"type": "string"}},
				"reason":  strProp("Why you need this, written so the user can decide."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				for _, field := range []string{"goal_id", "kind", "reason"} {
					if err := requireField(m, field); err != nil {
						return "", err
					}
				}
				body := stamp(bodyFrom(m, "goal_id", "kind", "payload", "reason"))
				return c.post(ctx, "/api/access-requests", body)
			},
		},
		{
			Name:        "podiom_ask_user",
			APIRoutes:   []string{"/api/agent-questions"},
			Description: "Ask the user a question when you are genuinely blocked on a decision that is theirs to make and cannot resolve it from the goal, the code, or sensible defaults. Only for unattended runs (goal planning/reviews and scheduled runs): the run does not wait — your question is recorded and, for a goal, pauses its reviews and shows on the goal page; the user's answer reaches your next session. Provide one or more questions, each with a short header, the question text, and (recommended) a few selectable options; the user can also type a free-text answer. In an interactive chat session, do NOT use this — ask the user directly instead." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"questions"}, map[string]any{
				"questions": map[string]any{
					"type":        "array",
					"description": "One or more questions to ask.",
					"items": map[string]any{
						"type":     "object",
						"required": []string{"question"},
						"properties": map[string]any{
							"header":       strProp("Short label for the question (a few words)."),
							"question":     strProp("The question text."),
							"multi_select": map[string]any{"type": "boolean", "description": "Allow selecting more than one option."},
							"options": map[string]any{
								"type":        "array",
								"description": "Selectable answers. Omit for a free-text-only question.",
								"items": map[string]any{
									"type":       "object",
									"required":   []string{"label"},
									"properties": map[string]any{"label": strProp("Option label."), "description": strProp("Optional clarifying detail.")},
								},
							},
						},
					},
				},
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "questions"); err != nil {
					return "", err
				}
				body := stamp(bodyFrom(m, "questions"))
				return c.post(ctx, "/api/agent-questions", body)
			},
		},
		{
			Name:      "podiom_request_user_action",
			APIRoutes: []string{"/api/goal-action-items"},
			Description: "Hand a step back to the user because only a human can do it — post from their personal account, sign or pay for something, make a call, do anything physical or off-machine. The item appears on the goal page with your instructions and a box for their verdict. " +
				"Your reviews are NOT paused: do not wait on it, carry on with the rest of the goal, and plan around it. Their verdict (Done / Couldn't do / Not doing) and note reach you at your next review. " +
				"Choose this over the alternatives deliberately: podiom_ask_user is for a DECISION that is theirs to make and it does pause your reviews; podiom_request_access is for a capability Podiom can wire for you (an MCP server, a skill, a credential); next_step is a move YOU will make. Writing the ask into a progress entry instead is wrong — nobody can respond to that. " +
				"Do not file an item you already have open (podiom_get_goal shows them) — chase it in your progress entry instead. Write instructions the user can follow without knowing anything about your plan: exact text to post, the URL, the timing, whatever they need." + workspaceFileProseGuidance,
			InputSchema: objectSchema([]string{"goal_id", "title", "instructions"}, map[string]any{
				"goal_id":      strProp("Goal id this action belongs to."),
				"title":        strProp("The one-line ask, imperative (e.g. \"Post the launch thread on r/selfhosted\")."),
				"instructions": strProp("Markdown the user can act on without further context: the steps, the exact content, links, timing."),
				"why":          strProp("One sentence on why this needs them rather than you."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				for _, field := range []string{"goal_id", "title", "instructions"} {
					if err := requireField(m, field); err != nil {
						return "", err
					}
				}
				body := stamp(bodyFrom(m, "goal_id", "title", "instructions", "why"))
				return c.post(ctx, "/api/goal-action-items", body)
			},
		},
		{
			Name:        "podiom_list_access_requests",
			APIRoutes:   []string{"/api/access-requests"},
			Description: "List access requests (optionally by goal and/or status: pending, approved, denied, executed, failed). Approved/denied entries carry the user's decision note — read it and act on it.",
			InputSchema: objectSchema(nil, map[string]any{
				"goal_id": strProp("Only requests for this goal."),
				"status":  strProp("Only requests with this status."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				q := url.Values{}
				if v := strings.TrimSpace(argString(m, "goal_id")); v != "" {
					q.Set("goal_id", v)
				}
				if v := strings.TrimSpace(argString(m, "status")); v != "" {
					q.Set("status", v)
				}
				path := "/api/access-requests"
				if len(q) > 0 {
					path += "?" + q.Encode()
				}
				return c.get(ctx, path)
			},
		},
	}
}
