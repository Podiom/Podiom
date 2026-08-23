package notify

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// bodyLimit caps a notification body. Bodies are built from domain fields rather
// than transcripts, but an error message or an agent-authored title can still be
// long, and a push payload is not the place for it: the notification navigates to
// the full context instead of carrying it.
const bodyLimit = 200

// answerBodyLimit caps a body carrying the agent's own closing words. The completion
// types are the one place a body is the agent's summary of what it did rather than a
// domain field, and a verdict plus its qualification runs past the general cap. iOS
// shows roughly four lines collapsed and more on long-press, so the extra text is
// reachable rather than wasted. Still a summary, not a transcript: the producer reads
// only the turn's final answer and bounds it before it reaches an Event.
const answerBodyLimit = 400

// titleLimit caps a notification title in bytes.
//
// The push relay rejects a longer title outright, which would lose the push entirely
// rather than shorten it — and titles interpolate agent names and goal titles, so a
// verbose goal would silently stop notifying about anything. Bytes rather than runes
// because that is what the relay counts.
const titleLimit = 200

// subjectFallback stands in when an agent name is unavailable — a session that
// was deleted, or a producer that had no agent in scope.
const subjectFallback = "Podiom"

// render produces the title and body for one event. Every notification's wording
// lives here, so a type's text can be read and changed in one place, and so no
// behaviour anywhere can depend on how a notification happens to be phrased.
//
// names carries the display strings the engine resolved from the store (agent,
// goal, schedule, task); each may be empty and rendering must cope.
// render produces the title and body for one event, both bounded so a long agent or goal
// name cannot produce a payload the relay refuses.
func render(ev Event, names displayNames) (string, string) {
	title, body := renderText(ev, names)
	return truncateBytes(title, titleLimit), body
}

func renderText(ev Event, names displayNames) (title, body string) {
	agent := ev.AgentName
	if agent == "" {
		agent = names.Agent
	}
	if agent == "" {
		agent = subjectFallback
	}
	detail := truncate(ev.Detail, bodyLimitFor(ev.Type))
	goal := firstNonEmpty(names.Goal, ev.GoalID)
	// A schedule is identified by its name, so there is no id to fall back on if
	// it was renamed or deleted between the run and the notification.
	schedule := firstNonEmpty(names.Schedule, ev.ScheduleName, "A schedule")
	task := firstNonEmpty(names.Task, ev.TaskID)

	switch ev.Type {
	case TypeSessionQuestion:
		return agent + " has a question", firstNonEmpty(detail, "Answer to let the agent continue.")
	case TypeSessionPermissionRequired:
		return agent + " needs approval", firstNonEmpty(detail, "A tool action is waiting for your decision.")
	case TypeSessionActionRequired:
		// Covers both a rate-limit fallback choice and a signed-out account; the
		// producer's detail says which, because the user's next step differs.
		return agent + " needs you", firstNonEmpty(detail, "A session is waiting on you to continue.")
	case TypeSessionExecutionFailed:
		return agent + " hit an error", firstNonEmpty(detail, "The session stopped before finishing.")

	case TypeScheduleStarted:
		return schedule + " started", detail
	case TypeScheduleSucceeded:
		return schedule + " completed", firstNonEmpty(detail, "The scheduled run finished successfully.")
	case TypeScheduleFailed:
		return schedule + " failed", firstNonEmpty(detail, "The scheduled run did not finish.")
	case TypeScheduleQuestion:
		return agent + " has a question", firstNonEmpty(detail,
			fmt.Sprintf("The %s run is waiting for an answer.", schedule))

	case TypeGoalRunStarted:
		return agent + " is working on " + quoted(goal, "a goal"), detail
	case TypeGoalRunSucceeded:
		return quoted(goal, "a goal") + " run finished", firstNonEmpty(detail, "The goal run finished.")
	case TypeGoalRunFailed:
		return quoted(goal, "a goal") + " run failed", firstNonEmpty(detail, "The goal run did not finish.")
	case TypeGoalProgress:
		return quoted(goal, "a goal") + " progressed", detail
	case TypeGoalMetricChanged:
		return quoted(goal, "a goal") + " metrics updated", detail
	case TypeGoalPlanChanged:
		return quoted(goal, "a goal") + " plan changed", detail
	case TypeGoalQuestion:
		return agent + " has a question", firstNonEmpty(detail,
			fmt.Sprintf("The %s goal is waiting for an answer.", quoted(goal, "a goal")))
	case TypeGoalActionRequested:
		return agent + " needs your help", firstNonEmpty(detail, "A step was handed back to you.")
	case TypeGoalAccessRequested:
		return agent + " requests access", firstNonEmpty(detail, "An access request is waiting for your decision.")
	case TypeGoalCompletionProposed:
		return fmt.Sprintf("%s thinks %s is complete", agent, quoted(goal, "a goal")),
			firstNonEmpty(detail, "Review the closing report to confirm.")
	case TypeGoalRateLimited:
		return quoted(goal, "a goal") + " is blocked", firstNonEmpty(detail,
			"The goal hit a rate limit and needs a new provider target.")
	case TypeGoalStatusChanged:
		return quoted(goal, "a goal") + " status changed", detail

	case TypeTaskStarted:
		return agent + " started " + quoted(task, "a task"), detail
	case TypeTaskCompleted:
		return quoted(task, "a task") + " completed", detail
	case TypeTaskReviewRequired:
		return quoted(task, "a task") + " is ready for review", firstNonEmpty(detail, "The agent finished its work on this task.")
	case TypeTaskFailed:
		return quoted(task, "a task") + " failed", firstNonEmpty(detail, "The task stopped before finishing.")

	case TypeSystemWarning:
		return "Podiom warning", detail
	}

	// An unregistered type never reaches here — the engine drops it before
	// rendering — so this is a registry entry whose wording was forgotten. Name it
	// rather than shipping a blank notification.
	return ev.Type, detail
}

// displayNames are the human-readable names the engine resolved for an event's
// scope ids. Any of them may be empty when the referenced row is gone.
type displayNames struct {
	Agent    string
	Goal     string
	Schedule string
	Task     string
}

// truncate shortens s to at most limit runes, marking that it was cut. It counts
// runes rather than bytes so a multi-byte character is never split in half.
func truncate(s string, limit int) string {
	s = strings.TrimSpace(s)
	// Collapse newlines: a notification body is one line of text, and an error
	// message spanning several would render unpredictably across platforms.
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return strings.TrimSpace(string(runes[:limit-1])) + "…"
}

// bodyLimitFor returns the body cap for a notification type. The completion types
// carry the agent's closing words and get more room than a body assembled from domain
// fields; see answerBodyLimit.
func bodyLimitFor(notifType string) int {
	switch notifType {
	case TypeTaskReviewRequired, TypeGoalRunSucceeded, TypeScheduleSucceeded:
		return answerBodyLimit
	}
	return bodyLimit
}

// truncateBytes shortens s to at most limit bytes without splitting a character.
func truncateBytes(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	// Leave room for the ellipsis, then step back to a rune boundary.
	cut := limit - len("…")
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return strings.TrimSpace(s[:cut]) + "…"
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// quoted wraps a name in typographic quotes, falling back to a neutral noun for
// the thing being named so a title never reads `“” run failed` when the row it
// referred to has been deleted.
func quoted(name, fallback string) string {
	if name == "" {
		return fallback
	}
	return "“" + name + "”"
}
