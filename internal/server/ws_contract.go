package server

import (
	"encoding/json"

	"github.com/Podiom/Podiom/internal/adapter"
	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/store"
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
}

// ServerMessage is the daemon-to-browser WebSocket contract.
type ServerMessage struct {
	Type        string                     `json:"type"`
	RequestID   string                     `json:"request_id,omitempty"`
	SessionID   string                     `json:"session_id,omitempty"`
	Agents      []store.Agent              `json:"agents,omitempty"`
	Sessions    []store.Session            `json:"sessions,omitempty"`
	ActiveTurns []ActiveTurnSummary        `json:"active_turns,omitempty"`
	Usage       []usage.Snapshot           `json:"usage,omitempty"`
	Session     *store.Session             `json:"session,omitempty"`
	Plan        *store.PlanInfo            `json:"plan,omitempty"`
	NextMessage string                     `json:"next_message,omitempty"`
	History     []store.Message            `json:"history,omitempty"`
	Message     *store.Message             `json:"message,omitempty"`
	Delta       string                     `json:"delta,omitempty"`
	Notice      string                     `json:"notice,omitempty"`
	Request     *adapter.PermissionRequest `json:"request,omitempty"`
	Input       *adapter.UserInputRequest  `json:"input,omitempty"`
	TurnState   *TurnState                 `json:"turn_state,omitempty"`
	Error       string                     `json:"error,omitempty"`
	// Dream fields carry a "dream_state" message: AgentName identifies the agent,
	// DreamPhase is the current phase (gathering|distilling|integrating|done|
	// noop|error), and Dream is the finished journal row on the terminal "done".
	AgentName  string       `json:"agent_name,omitempty"`
	DreamPhase string       `json:"dream_phase,omitempty"`
	Dream      *store.Dream `json:"dream,omitempty"`
}

func decodeClientMessage(data []byte) (ClientMessage, error) {
	var msg ClientMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return ClientMessage{}, err
	}
	return msg, nil
}
