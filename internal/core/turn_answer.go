package core

import (
	"context"
	"regexp"
	"strings"
)

// answerHardLimit bounds the text TurnAnswer returns. The display cap belongs to
// whoever renders it; this exists so an agent that ends a turn with a long report
// cannot make the value that carries it arbitrarily large.
const answerHardLimit = 1000

// TurnAnswer returns the agent's closing words for a session's latest turn as one
// line of plain text, or "" when there are none.
//
// Best-effort by design: this feeds a notification's wording, and wording must never
// be the reason a turn's completion goes unreported. A deleted session, a canceled
// context or a turn that only thought all yield an empty string rather than an error,
// leaving the caller's static fallback in place.
func (c *Core) TurnAnswer(ctx context.Context, sessionID string) string {
	if c == nil || c.store == nil || sessionID == "" {
		return ""
	}
	answer, err := c.store.LatestTurnAnswer(ctx, sessionID)
	if err != nil {
		return ""
	}
	return flattenAnswer(answer)
}

var (
	// A fenced block, including an unterminated one — an answer cut short mid-fence
	// should lose the fence rather than keep it as prose.
	answerCodeFence = regexp.MustCompile("(?s)```.*?(```|$)")
	// Leading block markers: heading, quote, bullet or ordered-list item.
	answerLineMarker = regexp.MustCompile(`^\s*(#{1,6}\s+|>\s*|[-*+]\s+|\d+[.)]\s+)`)
	// Emphasis and inline code, unwrapped as pairs rather than stripped as loose
	// characters. Underscore italics are deliberately left alone: snake_case
	// identifiers are far more common in an agent's answer than _emphasis_, and
	// stripping loose underscores would turn session_id into sessionid.
	answerEmphasis = []*regexp.Regexp{
		regexp.MustCompile(`\*\*(.+?)\*\*`),
		regexp.MustCompile(`\*(.+?)\*`),
		regexp.MustCompile("`(.+?)`"),
	}
)

// flattenAnswer turns an agent's markdown answer into the single line of prose a
// notification body needs. Agents lead with a verdict and follow with bullets, so
// without this a body reads `**Done.** - changed src/auth.go - added test`.
func flattenAnswer(s string) string {
	s = answerCodeFence.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = answerLineMarker.ReplaceAllString(line, "")
	}
	s = strings.Join(lines, "\n")
	for _, pattern := range answerEmphasis {
		s = pattern.ReplaceAllString(s, "$1")
	}
	return truncateRunes(oneLine(s), answerHardLimit)
}
