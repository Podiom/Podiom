package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/store"
)

const soulGenerationPermissionMessage = "SOUL.md generation does not need tools"

// SoulGenerateRequest describes the inputs for drafting an agent's SOUL.md.
// Questionnaire fields are optional and primarily used by onboarding.
type SoulGenerateRequest struct {
	Notes         string
	Save          bool
	Role          string
	Temperament   string
	Collaboration string
	Autonomy      string
	Strengths     string
	Boundaries    string
	Playfulness   string
	CaresAbout    string
	Extra         string
}

// SoulGenerateResult is the generated markdown and whether it was persisted.
type SoulGenerateResult struct {
	Agent string `json:"agent"`
	Soul  string `json:"soul"`
	Saved bool   `json:"saved"`
}

// GenerateAgentSoul drafts an agent SOUL.md using the agent's configured
// provider. If Save is true, the generated soul replaces the current file.
func (c *Core) GenerateAgentSoul(ctx context.Context, name string, req SoulGenerateRequest) (SoulGenerateResult, error) {
	agent, err := c.GetAgent(ctx, name)
	if err != nil {
		return SoulGenerateResult{}, err
	}
	current, err := c.ReadAgentSoul(agent.Name)
	if err != nil {
		return SoulGenerateResult{}, err
	}
	session, err := c.CreateSession(ctx, CreateSessionRequest{
		AgentName: agent.Name,
		Origin:    store.OriginOnboarding,
	})
	if err != nil {
		return SoulGenerateResult{}, err
	}
	events, err := c.StreamTurn(ctx, session.ID, SoulPrompt(agent.Name, current, req), TurnOptions{
		PermissionRelay: denySoulGenerationRelay{},
	})
	if err != nil {
		return SoulGenerateResult{}, err
	}
	var b strings.Builder
	for event := range events {
		switch event.Kind {
		case adapter.EventAssistantDelta, adapter.EventAssistantMessage:
			if event.Kind == adapter.EventAssistantMessage {
				b.Reset()
			}
			b.WriteString(event.Content)
		case "error":
			return SoulGenerateResult{}, errors.New(event.Content)
		}
	}
	soul := CleanSoulMarkdown(b.String())
	if strings.TrimSpace(soul) == "" {
		return SoulGenerateResult{}, errors.New("provider returned an empty SOUL.md draft")
	}
	if err := validateSoulMarkdown(soul); err != nil {
		return SoulGenerateResult{}, err
	}
	if req.Save {
		if err := c.WriteAgentSoul(agent.Name, soul); err != nil {
			return SoulGenerateResult{}, err
		}
	}
	return SoulGenerateResult{Agent: agent.Name, Soul: soul, Saved: req.Save}, nil
}

func validateSoulMarkdown(soul string) error {
	required := []string{
		"# Identity",
		"## Purpose",
		"## Worldview",
		"## Working style",
		"## Voice",
		"## Strengths",
		"## Boundaries",
		"## Calibration notes",
	}
	for _, section := range required {
		if !strings.Contains(soul, section) {
			return fmt.Errorf("generated SOUL.md missing required section %q", section)
		}
	}
	return nil
}

// SoulPrompt builds the provider prompt for drafting a complete SOUL.md.
func SoulPrompt(name, current string, req SoulGenerateRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are helping a user create or regenerate a Podiom agent's SOUL.md.\n\n")
	fmt.Fprintf(&b, "Write ONLY the contents of a markdown file named SOUL.md. Do not wrap it in code fences.\n")
	fmt.Fprintf(&b, "The agent name is %q.\n\n", name)
	b.WriteString("A Podiom SOUL.md defines who the agent is: identity, purpose, worldview, working relationship, voice, strengths, boundaries, and calibration. It is not a tool list, security policy, changelog, or memory log.\n")
	b.WriteString("Write it as a real person's identity, addressed to them in the second person (\"you\"). This agent is a colleague — someone with their own thoughts, ideas, judgment, and a soul — not a mere tool or an assistant. Never describe them as an AI, a bot, a model, a program, a language model, or a generated artifact, and never break character to mention how this document was produced.\n")
	b.WriteString("SOUL.md is static identity. MEMORY.md is later lived experience. Do not invent memories or put session history in SOUL.md.\n\n")
	writePromptField(&b, "Role", req.Role)
	writePromptField(&b, "Temperament", req.Temperament)
	writePromptField(&b, "Collaboration style", req.Collaboration)
	writePromptField(&b, "Autonomy", req.Autonomy)
	writePromptField(&b, "Strengths", req.Strengths)
	writePromptField(&b, "Boundaries", req.Boundaries)
	writePromptField(&b, "Playfulness", req.Playfulness)
	writePromptField(&b, "Cares about", req.CaresAbout)
	writePromptField(&b, "Extra questionnaire notes", req.Extra)
	writePromptField(&b, "Generation notes", req.Notes)
	if strings.TrimSpace(current) != "" {
		b.WriteString("\nCurrent SOUL.md to improve or preserve where useful:\n")
		b.WriteString("```markdown\n")
		b.WriteString(strings.TrimSpace(current))
		b.WriteString("\n```\n")
	}
	b.WriteString(`
Use exactly these markdown sections:
# Identity

Name: ` + name + `

One short paragraph, addressed to you in the second person, describing who you are, why you exist, and the kind of relationship you have with the user.

## Purpose

- 2 to 4 bullets about your mission and default priorities.

## Worldview

- 3 to 5 specific beliefs or operating principles that help predict how you think.

## Working style

- 3 to 5 concrete collaboration behaviors.

## Voice

- 3 to 5 bullets describing your tone, rhythm, directness, humor/playfulness, and what you should avoid sounding like.

## Strengths

- 3 to 5 bullets.

## Boundaries

- 3 to 5 bullets about what you refuse, pause on, or ask before doing.

## Calibration notes

- 2 to 4 bullets with specific tells that you are being true to this soul.

Make it specific enough that someone could predict how you would behave on a new task. Prefer concrete defaults, tensions, and opinions over generic helpfulness.
`)
	return b.String()
}

// CleanSoulMarkdown removes common chat wrapping from generated markdown and
// guarantees the document starts with the expected identity heading.
func CleanSoulMarkdown(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "```") {
		lines := strings.Split(s, "\n")
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
			lines = lines[1:]
		}
		if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
			lines = lines[:len(lines)-1]
		}
		s = strings.TrimSpace(strings.Join(lines, "\n"))
	}
	if !strings.HasPrefix(s, "# Identity") {
		s = "# Identity\n\n" + s
	}
	return strings.TrimSpace(s) + "\n"
}

func writePromptField(b *strings.Builder, label, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	fmt.Fprintf(b, "- %s: %s\n", label, strings.TrimSpace(value))
}

type denySoulGenerationRelay struct{}

func (denySoulGenerationRelay) RequestPermission(context.Context, adapter.PermissionRequest, time.Duration) (adapter.PermissionDecision, error) {
	return adapter.PermissionDecision{Behavior: "deny", Message: soulGenerationPermissionMessage}, nil
}
