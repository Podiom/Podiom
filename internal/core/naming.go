package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/store"
)

const namingTimeout = 45 * time.Second

type namingPayload struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

func (c *Core) autoNameSessionBackground(sessionID string) {
	ctx, cancel := context.WithTimeout(context.Background(), namingTimeout)
	defer cancel()
	_, _ = c.AutoNameSession(ctx, sessionID)
}

// AutoNameSession generates a concise name and description for a session once
// the first exchange exists. It is safe to call repeatedly.
func (c *Core) AutoNameSession(ctx context.Context, sessionID string) (store.Session, error) {
	sess, err := c.store.GetSession(ctx, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if sess.Name != "" || sess.Description != "" {
		return sess, nil
	}
	history, err := c.store.ListMessages(ctx, sessionID)
	if err != nil {
		return store.Session{}, err
	}
	if len(history) < 2 {
		return sess, nil
	}

	payload := c.generateNameWithModel(ctx, sess, history)
	if payload.Name == "" {
		payload = fallbackName(history)
	}
	return c.store.UpdateSessionMetadata(ctx, sessionID, payload.Name, payload.Description, true)
}

func (c *Core) generateNameWithModel(ctx context.Context, sess store.Session, history []store.Message) namingPayload {
	events, err := c.adapter.SendTurn(ctx, adapter.TurnRequest{
		SessionID: sess.ID + "-naming",
		Handle:    adapter.Handle{Provider: sess.Provider},
		Message: "Generate only compact JSON for the Podiom session transcript below: " +
			`{"name":"short title","description":"one sentence"}. ` +
			"Use six words or fewer for name and 140 characters or fewer for description.\n\n" +
			transcript(history),
		Settings: adapter.TurnSettings{
			AgentName:         sess.AgentName,
			Profile:           sess.Profile,
			ProfileDir:        c.profileDir(sess.Provider, sess.Profile),
			Model:             sess.Model,
			Effort:            "low",
			PermissionMode:    sess.PermissionMode,
			WorkspaceDir:      c.AgentPaths(sess.AgentName).Workspace,
			PermissionTimeout: c.permissionTimeout(),
		},
	})
	if err != nil {
		return namingPayload{}
	}
	var text strings.Builder
	for event := range events {
		switch event.Kind {
		case adapter.EventAssistantDelta:
			text.WriteString(event.Content)
		case adapter.EventAssistantMessage:
			text.Reset()
			text.WriteString(event.Content)
		}
	}
	return parseNamingPayload(text.String())
}

func transcript(history []store.Message) string {
	var b strings.Builder
	for _, msg := range history {
		if !isConversationMessage(msg) {
			continue
		}
		fmt.Fprintf(&b, "%s: %s\n", msg.Role, oneLine(msg.Content))
	}
	return b.String()
}

func parseNamingPayload(raw string) namingPayload {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var payload namingPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return namingPayload{}
	}
	payload.Name = truncateWords(strings.TrimSpace(payload.Name), 6)
	payload.Description = truncateRunes(strings.TrimSpace(payload.Description), 140)
	return payload
}

func fallbackName(history []store.Message) namingPayload {
	var user, assistant string
	for _, msg := range history {
		if !isConversationMessage(msg) {
			continue
		}
		switch msg.Role {
		case store.RoleUser:
			if user == "" {
				user = msg.Content
			}
		case store.RoleAssistant:
			if assistant == "" {
				assistant = msg.Content
			}
		}
	}
	name := truncateWords(strings.Trim(user, " \n\t.,:;!?"), 6)
	if name == "" {
		name = "Untitled Session"
	}
	description := truncateRunes(fmt.Sprintf("Started with: %s", oneLine(user)), 140)
	if assistant != "" {
		description = truncateRunes(fmt.Sprintf("%s Response: %s", description, oneLine(assistant)), 140)
	}
	return namingPayload{Name: name, Description: description}
}

func truncateWords(s string, max int) string {
	words := strings.Fields(s)
	if len(words) <= max {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:max], " ")
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max-1]) + "…"
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
