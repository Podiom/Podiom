package server

import (
	"encoding/json"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/core"
	"github.com/Podiom/Podiom/internal/store"
	"github.com/Podiom/Podiom/internal/tokenmeter"
	"github.com/Podiom/Podiom/internal/usage"
)

// ClientMessage is the browser-to-daemon WebSocket contract.
type ClientMessage struct {
	Type                           string                      `json:"type"`
	RequestID                      string                      `json:"request_id,omitempty"`
	AgentName                      string                      `json:"agent_name,omitempty"`
	SessionID                      string                      `json:"session_id,omitempty"`
	Message                        string                      `json:"message,omitempty"`
	Provider                       config.Provider             `json:"provider,omitempty"`
	Profile                        string                      `json:"profile,omitempty"`
	Model                          string                      `json:"model,omitempty"`
	Effort                         string                      `json:"effort,omitempty"`
	ProjectID                      string                      `json:"project_id,omitempty"`
	PermissionMode                 config.PermissionMode       `json:"permission_mode,omitempty"`
	CreatePlanBeforeImplementation bool                        `json:"create_plan_before_implementation,omitempty"`
	Feedback                       string                      `json:"feedback,omitempty"`
	Decision                       *adapter.PermissionDecision `json:"decision,omitempty"`
	Input                          *adapter.UserInputDecision  `json:"input,omitempty"`
	FallbackDecision               *core.FallbackDecision      `json:"fallback_decision,omitempty"`
}

// ServerMessage is the daemon-to-browser WebSocket contract.
type ServerMessage struct {
	Type        string                       `json:"type"`
	RequestID   string                       `json:"request_id,omitempty"`
	SessionID   string                       `json:"session_id,omitempty"`
	Agents      []store.Agent                `json:"agents,omitempty"`
	Sessions    []store.Session              `json:"sessions,omitempty"`
	ActiveTurns []ActiveTurnSummary          `json:"active_turns,omitempty"`
	Usage       []usage.Snapshot             `json:"usage,omitempty"`
	Session     *store.Session               `json:"session,omitempty"`
	Plan        *store.PlanInfo              `json:"plan,omitempty"`
	NextMessage string                       `json:"next_message,omitempty"`
	History     []store.Message              `json:"history,omitempty"`
	Message     *store.Message               `json:"message,omitempty"`
	Delta       string                       `json:"delta,omitempty"`
	Notice      string                       `json:"notice,omitempty"`
	Request     *adapter.PermissionRequest   `json:"request,omitempty"`
	Input       *adapter.UserInputRequest    `json:"input,omitempty"`
	Fallback    *core.FallbackRequest        `json:"fallback,omitempty"`
	NativeAgent *adapter.NativeAgentActivity `json:"native_agent,omitempty"`
	TurnState   *TurnState                   `json:"turn_state,omitempty"`
	Context     *ContextUsage                `json:"context,omitempty"`
	// SessionUsage is a session's cumulative billed-token total expressed as an
	// estimated share of the 5-hour and weekly limits. Sent with a "session"
	// message (on open) and pushed as a "session_usage" message after each turn.
	SessionUsage *tokenmeter.Estimate `json:"session_usage,omitempty"`
	Error        string               `json:"error,omitempty"`
	// Dream fields carry a "dream_state" message: AgentName identifies the agent,
	// DreamPhase is the current phase (gathering|distilling|integrating|done|
	// noop|error), and Dream is the finished journal row on the terminal "done".
	AgentName  string       `json:"agent_name,omitempty"`
	DreamPhase string       `json:"dream_phase,omitempty"`
	Dream      *store.Dream `json:"dream,omitempty"`
	// GoalEvent carries a "goal_event" broadcast: one appended goal-timeline
	// entry, fanned out to every live connection so open Goals views refresh
	// and attention badges update without polling.
	GoalEvent *store.GoalEvent `json:"goal_event,omitempty"`
}

// ContextUsage is the live context-window utilization for a session: how many
// tokens the last request used versus the model's window. Drives the composer's
// context ring. Max is 0 when the provider hasn't reported a window yet.
type ContextUsage struct {
	Used int64 `json:"used"`
	Max  int64 `json:"max"`
}

func decodeClientMessage(data []byte) (ClientMessage, error) {
	var msg ClientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return ClientMessage{}, err
	}
	return msg, nil
}
