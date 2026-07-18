package core

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/adapter"
)

// userProfileMaxBytes caps USER.md at write time. The file is injected into
// every agent's context on every turn, so an oversized profile would tax every
// session; 32 KB is far beyond any reasonable profile.
const userProfileMaxBytes = 32 * 1024

// ReadUserProfile returns the contents of the app-wide USER.md, or empty string
// if no profile has been written yet.
func (c *Core) ReadUserProfile() (string, error) {
	data, err := os.ReadFile(c.paths.UserMD)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read USER.md: %w", err)
	}
	return string(data), nil
}

// WriteUserProfile overwrites the app-wide USER.md. The write is atomic because
// a save from the UI can race a compose reading the file for a starting turn.
func (c *Core) WriteUserProfile(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return errors.New("user profile content is empty")
	}
	if len(trimmed) > userProfileMaxBytes {
		return fmt.Errorf("user profile exceeds %d bytes", userProfileMaxBytes)
	}
	return writeFileAtomic(c.paths.UserMD, []byte(trimmed+"\n"), 0o644)
}

// DeleteUserProfile removes USER.md so agents stop receiving the profile layer.
func (c *Core) DeleteUserProfile() error {
	if err := os.Remove(c.paths.UserMD); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete USER.md: %w", err)
	}
	return nil
}

const interviewGateMessage = "This session may only use podiom_ask_profile_question and podiom_submit_user_profile."

type InterviewTopic string

const (
	InterviewTopicIdentityContext   InterviewTopic = "identity_context"
	InterviewTopicCommunication     InterviewTopic = "communication"
	InterviewTopicOutputPreferences InterviewTopic = "output_preferences"
	InterviewTopicTechnicalDepth    InterviewTopic = "technical_depth"
	InterviewTopicCollaboration     InterviewTopic = "collaboration"
)

var RequiredInterviewTopics = []InterviewTopic{
	InterviewTopicIdentityContext,
	InterviewTopicCommunication,
	InterviewTopicOutputPreferences,
	InterviewTopicTechnicalDepth,
	InterviewTopicCollaboration,
}

func ValidInterviewTopic(topic InterviewTopic) bool {
	for _, candidate := range RequiredInterviewTopics {
		if topic == candidate {
			return true
		}
	}
	return false
}

// UserProfileInterviewPrompt builds the opening prompt for a "Get to know me"
// interview session. Podiom owns the interview state; the provider may adapt
// questions, but it must ask and submit through the dedicated internal tools.
func UserProfileInterviewPrompt(current string) string {
	var b strings.Builder
	b.WriteString("You are conducting a short, friendly interview to write USER.md: a profile of the human you are talking to. USER.md is injected into every Podiom agent's context so all agents understand who this person is and how they like to work. It is about the USER — it is not SOUL.md and says nothing about you.\n\n")
	b.WriteString("Interview rules:\n")
	b.WriteString("- Ask exactly one question at a time by calling podiom_ask_profile_question. Never use a provider-native question tool and never ask in plain message text.\n")
	b.WriteString("- The first five questions must cover each required topic once: identity_context, communication, output_preferences, technical_depth, and collaboration. Podiom enforces this.\n")
	b.WriteString("- Give each question 3 to 5 concrete, distinct selectable options. Adapt later questions to earlier answers. After the five required topics, ask at most three useful follow-ups.\n")
	b.WriteString("- Use no other tools. Do not read or write files, browse, or emit a Markdown profile as assistant text.\n")
	b.WriteString("- Once the five required topics are covered and you have enough detail, call podiom_submit_user_profile with labeled facts. Values must be concise facts or directives, not sentences referring to the user as they/them. Podiom renders the final Markdown.\n")
	b.WriteString("- After podiom_submit_user_profile succeeds, stop.\n")
	if strings.TrimSpace(current) != "" {
		b.WriteString("\nA USER.md already exists. Treat this interview as a refresh: reconfirm every required topic, keep what still holds, and update what the new answers change.\n")
		b.WriteString("```markdown\n")
		b.WriteString(strings.TrimSpace(current))
		b.WriteString("\n```\n")
	}
	return b.String()
}

// UserProfileFact is one server-rendered labeled fact in USER.md.
type UserProfileFact struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// UserProfileDraft is the structured payload accepted from the interview MCP.
// The provider supplies facts; Podiom owns all Markdown headings and layout.
type UserProfileDraft struct {
	IdentityContext   []UserProfileFact `json:"identity_context"`
	Communication     []UserProfileFact `json:"communication"`
	OutputPreferences []UserProfileFact `json:"output_preferences"`
	TechnicalContext  []UserProfileFact `json:"technical_context"`
	WorkingTogether   []UserProfileFact `json:"working_together"`
}

// RenderUserProfileDraft validates and renders a deterministic, pronoun-free
// USER.md draft. Values are flattened to one line so a tool argument cannot
// inject its own Markdown structure.
func RenderUserProfileDraft(draft UserProfileDraft) (string, error) {
	sections := []struct {
		heading string
		facts   []UserProfileFact
	}{
		{"Identity and context", draft.IdentityContext},
		{"Communication", draft.Communication},
		{"Output preferences", draft.OutputPreferences},
		{"Technical context", draft.TechnicalContext},
		{"Working together", draft.WorkingTogether},
	}
	var b strings.Builder
	b.WriteString(userProfileHeading + "\n")
	for _, section := range sections {
		if len(section.facts) == 0 || len(section.facts) > 5 {
			return "", fmt.Errorf("section %q must contain 1 to 5 facts", section.heading)
		}
		b.WriteString("\n## " + section.heading + "\n\n")
		seen := map[string]bool{}
		for _, fact := range section.facts {
			label := flattenProfileFact(fact.Label)
			value := flattenProfileFact(fact.Value)
			if label == "" || value == "" {
				return "", fmt.Errorf("section %q contains a blank label or value", section.heading)
			}
			if len(label) > 64 || len(value) > 320 {
				return "", fmt.Errorf("section %q contains an oversized fact", section.heading)
			}
			key := strings.ToLower(label)
			if seen[key] {
				return "", fmt.Errorf("section %q contains duplicate label %q", section.heading, label)
			}
			seen[key] = true
			fmt.Fprintf(&b, "- **%s:** %s\n", label, value)
		}
	}
	return b.String(), nil
}

func flattenProfileFact(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

// InterviewGateRelay mechanically limits an interview session to its two
// internal tools. Codex calls this relay directly; Claude reaches the same gate
// through the daemon's HTTP permission callback.
type InterviewGateRelay struct {
	log *slog.Logger
}

func NewInterviewGateRelay(loggers ...*slog.Logger) *InterviewGateRelay {
	log := slog.Default()
	if len(loggers) > 0 && loggers[0] != nil {
		log = loggers[0]
	}
	return &InterviewGateRelay{log: log}
}

func (r *InterviewGateRelay) RequestPermission(_ context.Context, req adapter.PermissionRequest, _ time.Duration) (adapter.PermissionDecision, error) {
	if IsInterviewTool(req.ToolName) {
		r.log.Info("interview gate allowed tool", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName)
		return adapter.PermissionDecision{Behavior: "allow", UpdatedInput: req.Input}, nil
	}
	r.log.Info("interview gate denied tool", "event", "permission", "turn", req.TurnID, "request", req.ID, "tool_name", req.ToolName)
	return adapter.PermissionDecision{Behavior: "deny", Message: interviewGateMessage}, nil
}

func IsInterviewTool(name string) bool {
	name = strings.ToLower(name)
	return strings.Contains(name, "podiom_ask_profile_question") || strings.Contains(name, "podiom_submit_user_profile")
}

// userProfileHeading is the required top-level heading of USER.md; the frontend
// also uses it to recognize the finished draft in the interview's final message.
const userProfileHeading = "# About the user"

// CleanUserProfileMarkdown removes common chat wrapping from generated profile
// markdown and guarantees the document starts with the expected heading.
func CleanUserProfileMarkdown(raw string) string {
	// Strip wrapping fence lines before dropping the preamble: a preamble like
	// "Here you go:\n```markdown\n# About the user…" carries its opening fence
	// inside the preamble, and only stripping the trailing fence first keeps the
	// closing "```" from dangling at the end of the sliced document.
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[0]), "```") {
		lines = lines[1:]
	}
	if len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
		lines = lines[:len(lines)-1]
	}
	s := strings.TrimSpace(strings.Join(lines, "\n"))
	if idx := strings.Index(s, userProfileHeading); idx > 0 {
		// Drop any preamble the model added before the document proper.
		s = s[idx:]
	} else if idx < 0 && s != "" {
		s = userProfileHeading + "\n\n" + s
	}
	return strings.TrimSpace(s) + "\n"
}
