package core

import (
	"errors"
	"fmt"
	"os"
	"strings"
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

// UserProfileInterviewPrompt builds the opening prompt for a "Get to know me"
// interview session: the agent interviews the user with its native question
// tool, then emits the finished USER.md as its final message.
func UserProfileInterviewPrompt(current string) string {
	var b strings.Builder
	b.WriteString("You are conducting a short, friendly interview to write USER.md: a profile of the human you are talking to. USER.md is injected into every Podiom agent's context so all agents understand who this person is and how they like to work. It is about the USER — it is not SOUL.md and says nothing about you.\n\n")
	b.WriteString("Interview rules:\n")
	b.WriteString("- Ask 5 to 8 questions total, strictly ONE question at a time, and ONLY through your question tool (the tool that presents selectable options). Never ask a question as plain message text.\n")
	b.WriteString("- Give each question 3 to 5 concrete, distinct selectable options. Keep questions light and quick to answer; the user should mostly click.\n")
	b.WriteString("- Adapt later questions to earlier answers instead of following a fixed script.\n")
	b.WriteString("- Cover, roughly: who they are (role, context, what they work on); how they like to be spoken to (tone, directness, formality); how much detail and what format they want in answers; their technical depth; how they want decisions and feedback handled; and anything that annoys them.\n")
	b.WriteString("- Use no other tools. Do not read or write any files. Do not browse.\n\n")
	b.WriteString("When the interview is complete, reply with ONLY the finished USER.md markdown — no code fences, no commentary before or after. Use exactly these sections:\n\n")
	b.WriteString("# About the user\n\nOne short paragraph introducing who this person is.\n\n")
	b.WriteString("## Who they are\n\n- 2 to 4 bullets: role, context, what they spend their time on.\n\n")
	b.WriteString("## How to communicate\n\n- 3 to 5 bullets: tone, directness, formality, technical depth to assume.\n\n")
	b.WriteString("## Output preferences\n\n- 3 to 5 bullets: verbosity, structure and format of answers, what to lead with.\n\n")
	b.WriteString("## Working together\n\n- 3 to 5 bullets: how they want decisions, feedback, and disagreement handled; what to avoid.\n\n")
	b.WriteString("Write the profile in the third person (\"they\"), specific enough that an agent reading it could predict how this person wants a new conversation to go.\n")
	if strings.TrimSpace(current) != "" {
		b.WriteString("\nA USER.md already exists. Treat this interview as a refresh: keep what still holds, update what the new answers change.\n")
		b.WriteString("```markdown\n")
		b.WriteString(strings.TrimSpace(current))
		b.WriteString("\n```\n")
	}
	return b.String()
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
