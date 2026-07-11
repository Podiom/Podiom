package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
)

// SlashResult reports whether a user message was handled as a slash command.
type SlashResult struct {
	Handled bool
	Session store.Session
	Notice  string
	// Compact marks a recognized /compact. The command is deferred to the
	// caller (server), which runs it under the active-turn registry so it is
	// guarded against concurrent turns and can stream progress like a real turn.
	Compact bool
}

// HandleSlashCommand applies session-scoped slash commands without appending
// them to canonical chat history.
func (c *Core) HandleSlashCommand(ctx context.Context, sessionID, input string) (SlashResult, error) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return SlashResult{}, nil
	}
	command, arg, _ := strings.Cut(trimmed, " ")
	command = strings.ToLower(strings.TrimPrefix(command, "/"))
	arg = strings.TrimSpace(arg)

	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return SlashResult{}, err
	}

	switch command {
	case "model":
		if arg == "" {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /model <name>"}, nil
		}
		updated, err := c.store.UpdateSessionSettings(ctx, sess.ID, arg, sess.Effort, sess.PermissionMode)
		return SlashResult{Handled: true, Session: updated, Notice: fmt.Sprintf("Model set to %s", arg)}, err
	case "effort":
		if !validEffort(arg) {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /effort <provider-supported level>"}, nil
		}
		updated, err := c.store.UpdateSessionSettings(ctx, sess.ID, sess.Model, arg, sess.PermissionMode)
		return SlashResult{Handled: true, Session: updated, Notice: fmt.Sprintf("Effort set to %s", arg)}, err
	case "permission":
		mode := config.PermissionMode(arg)
		if mode != config.PermissionApprove && mode != config.PermissionYolo {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /permission approve|yolo"}, nil
		}
		updated, err := c.store.UpdateSessionSettings(ctx, sess.ID, sess.Model, sess.Effort, mode)
		notice := fmt.Sprintf("Permission mode set to %s", mode)
		if mode == config.PermissionYolo {
			notice = "Permission mode set to yolo — whole-machine access, every tool call auto-approved. The workspace is NOT a sandbox (R8.31). Switch back with /permission approve."
		}
		return SlashResult{Handled: true, Session: updated, Notice: notice}, err
	case "profile":
		if arg == "" {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /profile <name|default>"}, nil
		}
		provider := sess.Provider
		profile := arg
		if arg == "default" {
			profile = ""
		} else {
			got, ok := c.profiles[arg]
			if !ok {
				return SlashResult{Handled: true, Session: sess, Notice: fmt.Sprintf("Unknown profile %q", arg)}, nil
			}
			provider = got.Provider
		}
		updated, err := c.switchSessionTarget(ctx, sess, provider, profile)
		if err != nil {
			return SlashResult{Handled: true, Session: sess, Notice: err.Error()}, err
		}
		return SlashResult{Handled: true, Session: updated, Notice: fmt.Sprintf("Profile set to %s; next turn will replay history", profileNotice(updated.Profile))}, nil
	case "name":
		if arg == "" {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /name <session name>"}, nil
		}
		updated, err := c.store.UpdateSessionMetadata(ctx, sess.ID, arg, sess.Description, false)
		return SlashResult{Handled: true, Session: updated, Notice: "Session name updated"}, err
	case "describe":
		if arg == "" {
			return SlashResult{Handled: true, Session: sess, Notice: "Usage: /describe <session description>"}, nil
		}
		updated, err := c.store.UpdateSessionMetadata(ctx, sess.ID, sess.Name, arg, false)
		return SlashResult{Handled: true, Session: updated, Notice: "Session description updated"}, err
	case "compact":
		return SlashResult{Handled: true, Compact: true, Session: sess}, nil
	case "help":
		return SlashResult{
			Handled: true,
			Session: sess,
			Notice:  "/model <name>, /effort <level>, /profile <name|default>, /permission <approve|yolo>, /name <text>, /describe <text>, /compact",
		}, nil
	default:
		return SlashResult{Handled: true, Session: sess, Notice: fmt.Sprintf("Unknown command /%s. Try /help.", command)}, nil
	}
}

func profileNotice(profile string) string {
	if profile == "" {
		return "default"
	}
	return profile
}

func validEffort(effort string) bool {
	return capabilities.HasEffort(capabilities.DefaultEfforts, effort)
}
