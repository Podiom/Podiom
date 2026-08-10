package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
)

// agentTools is the agent-facing surface for managing colleagues.
//
// The boundaries here cannot be expressed in notManageable: /api/agents and
// /api/agents/ are already covered by the list/get tools, and the coverage
// guardrail matches whole route patterns. So this comment is the record.
//
// Deliberately absent:
//   - Editing a SOUL. An agent's SOUL is its identity, the user wrote it, and it
//     persists into every future session. podiom_generate_agent_soul can give a
//     brand-new agent its first one; nothing here may overwrite an existing one.
//   - permission_mode on update. That is privilege, not preference: raising a
//     colleague's mode to yolo would route around the permission_mode access
//     request, which exists precisely to put a human on that decision.
//   - mcp_servers on update. podiom_assign_mcp_server already owns assignment one
//     server at a time; a whole-list replace here would silently unassign every
//     server the model forgot to include.
//   - Writing or clearing MEMORY.md. Memory accrues through the dream loop, and
//     the endpoint replaces the whole file — a clobber waiting to happen.
//   - Deleting an agent. core.DeleteAgent archives and then removes every session
//     that agent ever had, plus their attachments. The HTTP layer demands the
//     exact agent name as confirmation, which is a gate built for a human hand; a
//     boolean is not the right lock for it.
func agentTools(c *manageClient, agentName string) []mcpTool {
	return []mcpTool{
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
		{
			Name:      "podiom_create_agent",
			APIRoutes: []string{"/api/agents"},
			Description: "Create a new agent — a new colleague on this system. The agent starts with a blank identity skeleton and no memory; fill the identity in with podiom_generate_agent_soul right after. " +
				"Omit provider, profile, model, and effort to inherit the global defaults from podiom_get_config. " +
				"Create an agent only when the user has asked for a new colleague — do not staff the system on your own initiative.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name":            strProp("Agent name."),
				"provider":        strProp("Provider: claude or codex. Omit for the global default."),
				"profile":         strProp("Auth profile. Omit for the global default."),
				"model":           strProp("Model. Omit for the global default."),
				"effort":          strProp("Reasoning effort. Omit for the global default."),
				"permission_mode": strProp("approve, auto, or yolo. Omit for the global default."),
				"fallback":        strArrProp("Ordered fallback provider list."),
				"mcp_servers":     strArrProp("MCP server names to assign at creation."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "name", "provider", "profile", "model", "effort", "permission_mode", "fallback", "mcp_servers")
				return c.post(ctx, "/api/agents", body)
			},
		},
		{
			Name:      "podiom_update_agent",
			APIRoutes: []string{"/api/agents/"},
			Description: "Update an agent's run target: provider, profile, model, effort, or fallback chain. Only the fields you pass are changed. " +
				"To change which MCP servers an agent has, use podiom_assign_mcp_server. " +
				"An agent's identity (soul) and its permission mode cannot be changed here — those are the user's to set, in Settings → Agents.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name":     strProp("Agent name."),
				"provider": strProp("Provider: claude or codex."),
				"profile":  strProp("Auth profile. Empty string returns to the default."),
				"model":    strProp("Model. Empty string returns to the default."),
				"effort":   strProp("Reasoning effort. Empty string returns to the default."),
				"fallback": strArrProp("Ordered fallback provider list."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				body := bodyFrom(m, "provider", "profile", "model", "effort", "fallback")
				if len(body) == 0 {
					return "", fmt.Errorf("provide at least one field to change")
				}
				return c.patch(ctx, "/api/agents/"+url.PathEscape(argString(m, "name")), body)
			},
		},
		{
			Name:      "podiom_generate_agent_soul",
			APIRoutes: []string{"/api/agents/"},
			Description: "Write and save the identity (SOUL.md) of an agent that does not have one yet — the natural next step after podiom_create_agent. " +
				"Describe the colleague you want in the optional fields; Podiom generates the identity and saves it. " +
				"This only works while the agent's identity is still the blank starting skeleton: rewriting an identity someone has already written is the user's to do in Settings → Agents.",
			InputSchema: objectSchema([]string{"name"}, map[string]any{
				"name":          strProp("Agent name."),
				"notes":         strProp("Freeform notes about who this colleague should be."),
				"role":          strProp("What they do."),
				"temperament":   strProp("How they carry themselves."),
				"collaboration": strProp("How they work with others."),
				"autonomy":      strProp("How much they decide alone."),
				"strengths":     strProp("What they are good at."),
				"boundaries":    strProp("What they will not do."),
				"playfulness":   strProp("How light or serious they are."),
				"cares_about":   strProp("What matters to them."),
				"extra":         strProp("Anything else."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				if err := requireField(m, "name"); err != nil {
					return "", err
				}
				name := argString(m, "name")
				if err := requireUnwrittenSoul(ctx, c, name); err != nil {
					return "", err
				}
				body := bodyFrom(m, "notes", "role", "temperament", "collaboration", "autonomy", "strengths", "boundaries", "playfulness", "cares_about", "extra")
				// Always save: a draft the caller cannot persist is useless here.
				body["save"], _ = json.Marshal(true)
				return c.post(ctx, "/api/agents/"+url.PathEscape(name)+"/generate", body)
			},
		},
		{
			Name:      "podiom_read_agent_memory",
			APIRoutes: []string{"/api/agents/"},
			Description: "Read an agent's accumulated memory (MEMORY.md). Defaults to your own, which is injected into your context but capped — this shows you all of it. " +
				"Reading a colleague's memory before delegating tells you what they already know. Memory is written by Podiom's own consolidation, not by tools.",
			InputSchema: objectSchema(nil, map[string]any{
				"agent": strProp("Agent name; defaults to you."),
			}),
			Handler: func(ctx context.Context, args json.RawMessage) (string, error) {
				m, err := argMap(args)
				if err != nil {
					return "", err
				}
				agent := strings.TrimSpace(argString(m, "agent"))
				if agent == "" {
					agent = strings.TrimSpace(agentName)
				}
				if agent == "" {
					return "", requireField(m, "agent")
				}
				return c.get(ctx, "/api/agents/"+url.PathEscape(agent)+"/memory")
			},
		},
	}
}

// requireUnwrittenSoul refuses soul generation for an agent whose identity has
// already been written. The check runs before any write, so a refusal never
// leaves a half-overwritten identity behind.
//
// "Unwritten" cannot mean "empty": creating an agent scaffolds SOUL.md from a
// skeleton, so every agent has a non-blank soul from the moment it exists. The
// honest test is whether the file is still that untouched skeleton, which we can
// compare exactly because this is the same binary that writes it.
//
// The response embeds store.Agent, which has no JSON tags, so the key is the Go
// field name rather than snake_case — the same quirk filterTasks documents.
func requireUnwrittenSoul(ctx context.Context, c *manageClient, name string) error {
	raw, err := c.get(ctx, "/api/agents/"+url.PathEscape(name))
	if err != nil {
		return err
	}
	var detail struct {
		Soul string `json:"Soul"`
	}
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return fmt.Errorf("could not read agent %q before generating its identity: %w", name, err)
	}
	soul := strings.TrimSpace(detail.Soul)
	skeleton := strings.TrimSpace(strings.ReplaceAll(string(config.AgentSoulTemplate()), "{{agent_name}}", name))
	if soul == "" || soul == skeleton {
		return nil
	}
	return fmt.Errorf("agent %q already has an identity; rewriting an existing agent's SOUL is the user's to do in Settings → Agents", name)
}
