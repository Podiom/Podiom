// Package adapter defines the provider seam between Podiom core and local LLM
// CLIs. Phase 1 ships only a deterministic fake implementation; real Claude and
// Codex process handling lands in later phases.
package adapter

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/config"
	podiommcp "github.com/Podiom/Podiom/internal/mcp"
	"github.com/Podiom/Podiom/internal/store"
)

// Handle is a provider-owned resume token such as a Claude session ID or Codex
// threadId, annotated with the provider that owns it.
type Handle struct {
	Provider config.Provider
	ID       string
}

// StartRequest contains the provider-neutral data needed to create a backing
// CLI session or thread.
type StartRequest struct {
	SessionID          string
	AgentName          string
	Provider           config.Provider
	Profile            string
	ProfileDir         string
	Model              string
	Effort             string
	PermissionMode     config.PermissionMode
	WorkspaceDir       string
	ExtraWorkspaceDirs []string
	InstructionPath    string
	Instructions       []byte
	MCPServers         []podiommcp.Server
	MCPAllServers      []podiommcp.Server
}

// ResumeRequest asks an adapter to bind to an existing provider handle.
type ResumeRequest struct {
	SessionID string
	Handle    Handle
}

// TurnRequest sends one user turn to the active backing session.
type TurnRequest struct {
	SessionID string
	Handle    Handle
	Message   string
	History   []store.Message
	Settings  TurnSettings
	Relay     PermissionRelay
	Input     UserInputRelay
}

// TurnSettings are the current session settings needed by per-turn providers
// such as Claude.
type TurnSettings struct {
	AgentName          string
	Profile            string
	ProfileDir         string
	Model              string
	Effort             string
	PermissionMode     config.PermissionMode
	WorkspaceDir       string
	ExtraWorkspaceDirs []string
	InstructionPath    string
	PermissionTurnID   string
	PermissionTimeout  time.Duration
	// Unattended marks a run with no human at the keyboard (a scheduled run).
	// In approve mode this selects the "preapproved" policy (§7.7): permission
	// requests are resolved without a human — via AllowedTools natively on
	// Claude, and via the in-process Relay on Codex — never queued for a person.
	Unattended bool
	// AllowedTools is the preapproved allow-list for an unattended run. Tools not
	// listed are auto-denied. Empty means deny all side-effecting actions.
	AllowedTools  []string
	MCPServers    []podiommcp.Server
	MCPAllServers []podiommcp.Server
}

// RateStatus reports provider-exposed rate-limit utilization when available.
// UsedPercent is the max across all windows (kept for the ≥80% summary trigger);
// Windows carries the full per-window breakdown when the provider exposes it.
type RateStatus struct {
	UsedPercent float64
	Windows     []RateWindow
}

// RateWindow is one provider rate-limit window observed passively mid-turn.
type RateWindow struct {
	Key           string
	UsedPercent   float64
	ResetsAt      time.Time
	WindowSeconds int64
}

// ContextStatus reports how full the model's context window is for the active
// session, derived deterministically from the provider stream. UsedTokens is the
// size of the last request's prompt; MaxTokens is the model's context window (0
// when the provider does not report it and no limit could be inferred).
type ContextStatus struct {
	UsedTokens int64
	MaxTokens  int64
}

// EventKind classifies streamed adapter output.
type EventKind string

const (
	// EventAssistantDelta is an incremental assistant text chunk.
	EventAssistantDelta EventKind = "assistant_delta"
	// EventAssistantMessage is the final assistant message for the turn.
	EventAssistantMessage EventKind = "assistant_message"
	// EventPermissionRequest asks the client to approve or deny a tool action.
	EventPermissionRequest EventKind = "permission_request"
	// EventUserInputRequest asks the client to answer a provider clarification.
	EventUserInputRequest EventKind = "user_input_request"
	// EventHandleUpdated carries a replacement resumable provider handle.
	EventHandleUpdated EventKind = "handle_updated"
	// EventRateStatus carries provider rate-limit utilization.
	EventRateStatus EventKind = "rate_status"
	// EventContextStatus carries the active turn's context-window utilization:
	// how many tokens the last request consumed versus the model's window.
	EventContextStatus EventKind = "context_status"
	// EventRateLimited reports that the active turn cannot continue on this
	// backing target because the provider rate-limited it.
	EventRateLimited EventKind = "rate_limited"
	// EventTurnDone marks the end of a turn stream.
	EventTurnDone EventKind = "turn_done"
)

// Event is one streamed provider event.
type Event struct {
	Kind              EventKind
	Content           string
	Handle            *Handle
	PermissionRequest *PermissionRequest
	UserInputRequest  *UserInputRequest
	RateStatus        *RateStatus
	ContextStatus     *ContextStatus
}

// Adapter abstracts over provider process models: per-turn Claude processes and
// a long-lived Codex app-server both fit this start/resume/send/teardown shape.
type Adapter interface {
	Start(context.Context, StartRequest) (Handle, error)
	Resume(context.Context, ResumeRequest) (Handle, error)
	SendTurn(context.Context, TurnRequest) (<-chan Event, error)
	Teardown(context.Context, Handle) error
	Capabilities(context.Context, capabilities.Request) (capabilities.ProviderCapabilities, error)
}

// PermissionRequest is the provider-neutral approval payload surfaced to a user.
type PermissionRequest struct {
	ID          string          `json:"id"`
	TurnID      string          `json:"turn_id"`
	ToolName    string          `json:"tool_name"`
	ToolUseID   string          `json:"tool_use_id"`
	Description string          `json:"description,omitempty"`
	Input       json.RawMessage `json:"input"`
	ExpiresAt   time.Time       `json:"expires_at,omitempty"`
}

// PermissionDecision is returned to the provider permission mechanism.
type PermissionDecision struct {
	Behavior     string          `json:"behavior"`
	UpdatedInput json.RawMessage `json:"updatedInput,omitempty"`
	Message      string          `json:"message,omitempty"`
}

// PermissionRelay receives permission requests and waits for user decisions.
type PermissionRelay interface {
	RequestPermission(context.Context, PermissionRequest, time.Duration) (PermissionDecision, error)
}

// UserInputRequest is a provider-neutral clarification prompt surfaced to users.
type UserInputRequest struct {
	ID               string              `json:"id"`
	TurnID           string              `json:"turn_id,omitempty"`
	Provider         config.Provider     `json:"provider,omitempty"`
	ItemID           string              `json:"item_id,omitempty"`
	Questions        []UserInputQuestion `json:"questions"`
	AutoResolutionMS int64               `json:"auto_resolution_ms,omitempty"`
}

// UserInputQuestion is one prompt in a provider clarification request.
type UserInputQuestion struct {
	ID          string            `json:"id"`
	Header      string            `json:"header,omitempty"`
	Question    string            `json:"question"`
	Options     []UserInputOption `json:"options,omitempty"`
	MultiSelect bool              `json:"multi_select,omitempty"`
	IsOther     bool              `json:"is_other,omitempty"`
	IsSecret    bool              `json:"is_secret,omitempty"`
}

// UserInputOption is one selectable answer option.
type UserInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
}

// UserInputDecision maps question ids to one or more selected/freeform answers.
type UserInputDecision struct {
	Answers map[string][]string `json:"answers"`
}

// UserInputRelay receives clarification requests and waits for user answers.
type UserInputRelay interface {
	RequestUserInput(context.Context, UserInputRequest, time.Duration) (UserInputDecision, error)
}

func loggerOrDefault(log *slog.Logger) *slog.Logger {
	if log != nil {
		return log
	}
	return slog.Default()
}
