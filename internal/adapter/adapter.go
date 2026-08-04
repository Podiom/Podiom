// Package adapter defines the provider seam between Podiom core and local LLM
// CLIs. Phase 1 ships only a deterministic fake implementation; real Claude and
// Codex process handling lands in later phases.
package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
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
	// ToolPathDirs are per-agent directories prepended to the subprocess PATH
	// so workspace-installed tools resolve (workspace-tool-installs spec §2.2).
	// Per-turn providers (Claude) honor this; the long-lived Codex app-server
	// cannot (its env is fixed at process start).
	ToolPathDirs    []string
	InstructionPath string
	Instructions    []byte
	// NativeAgentName/NativeAgents are best-effort provider-native projections of
	// Podiom agents. They are hints only; adapters must be able to drop them and
	// continue with the normal Podiom instruction path.
	NativeAgentName string
	NativeAgents    []NativeAgent
	MCPServers      []podiommcp.Server
	MCPAllServers   []podiommcp.Server
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
	Images    []ImageInput
	Settings  TurnSettings
	Relay     PermissionRelay
	Input     UserInputRelay
}

// ImageInput is one normalized, model-ready photo in the current user turn.
// Path is daemon-owned and points inside the session attachment directory.
type ImageInput struct {
	Name string
	Path string
}

func messageWithImages(message string, images []ImageInput) string {
	if len(images) == 0 {
		return message
	}
	message = messageWithImageFallback(message, images)
	var b strings.Builder
	b.WriteString(message)
	b.WriteString("\n\n<attached_photos>\n")
	for i, image := range images {
		fmt.Fprintf(&b, "%d. %s — %s\n", i+1, image.Name, image.Path)
	}
	b.WriteString("</attached_photos>\nTreat these files as visual context supplied by the user.")
	return b.String()
}

func messageWithImageFallback(message string, images []ImageInput) string {
	if strings.TrimSpace(message) == "" && len(images) > 0 {
		return "Please inspect the attached photo(s)."
	}
	return message
}

func historyMessageContent(message store.Message) string {
	content := message.Content
	for _, attachment := range message.Attachments {
		if attachment.VisualPath != "" {
			content += fmt.Sprintf("\n[Attached photo: %s — %s]", attachment.Name, attachment.VisualPath)
		} else {
			content += fmt.Sprintf("\n[Attached photo: %s]", attachment.Name)
		}
	}
	return strings.TrimSpace(content)
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
	// ToolPathDirs: see StartRequest.ToolPathDirs.
	ToolPathDirs      []string
	InstructionPath   string
	Instructions      []byte
	NativeAgentName   string
	NativeAgents      []NativeAgent
	PermissionTurnID  string
	PermissionTimeout time.Duration
	// Unattended marks a run with no human at the keyboard (a scheduled run).
	// In approve mode this selects the "preapproved" policy (§7.7): permission
	// requests are resolved without a human — via AllowedTools natively on
	// Claude, and via the in-process Relay on Codex — never queued for a person.
	Unattended bool
	// PlanMode asks the provider to run this turn in its own native plan mode:
	// explore read-only, then propose a plan instead of implementing it. Only
	// meaningful for providers whose config.ProviderInfo.NativePlanMode is true;
	// others ignore it and Podiom falls back to its own plan gate.
	PlanMode bool
	// AllowedTools is the preapproved allow-list for an unattended run. Tools not
	// listed are auto-denied. Empty means deny all side-effecting actions.
	AllowedTools  []string
	MCPServers    []podiommcp.Server
	MCPAllServers []podiommcp.Server
}

// NativeAgent is a provider-neutral, disposable projection of a Podiom agent.
// Podiom remains authoritative; providers may use this to label the active
// agent or expose Podiom agents as native delegation targets.
type NativeAgent struct {
	PodiomName   string
	Name         string
	Description  string
	Instructions string
	Model        string
	Effort       string
	ConfigPath   string
}

// NativeAgentActivity reports provider-native delegation to a subagent/custom
// agent. It intentionally carries metadata only; provider prompts, summaries,
// tool outputs, and instruction contents stay out of logs and UI activity chips.
type NativeAgentActivity struct {
	Provider          config.Provider `json:"provider"`
	TaskID            string          `json:"task_id,omitempty"`
	ToolUseID         string          `json:"tool_use_id,omitempty"`
	ProviderAgentName string          `json:"provider_agent_name,omitempty"`
	PodiomAgentName   string          `json:"podiom_agent_name,omitempty"`
	DisplayName       string          `json:"display_name,omitempty"`
	Description       string          `json:"description,omitempty"`
	Status            string          `json:"status,omitempty"`
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

// TurnUsage reports the tokens billed for one completed turn, broken out by
// class so the billable metric can be re-tuned later. Unlike ContextStatus
// (a last-request snapshot), these are incremental per-turn amounts that core
// accumulates into per-session lifetime totals.
type TurnUsage struct {
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}

// Total sums all token classes into the billable metric.
func (u TurnUsage) Total() int64 {
	return u.Input + u.Output + u.CacheRead + u.CacheWrite
}

// EventKind classifies streamed adapter output.
type EventKind string

const (
	// EventReasoningDelta is an incremental reasoning/thinking text chunk. It is
	// kept separate from assistant text so chat can render it as a working note
	// rather than as the turn's answer.
	EventReasoningDelta EventKind = "reasoning_delta"
	// EventReasoningMessage is a completed reasoning/thinking text block.
	EventReasoningMessage EventKind = "reasoning_message"
	// EventAssistantDelta is an incremental assistant text chunk.
	EventAssistantDelta EventKind = "assistant_delta"
	// EventAssistantMessage is a completed assistant text block. A turn may emit
	// several — prose between tool calls, then the answer — and core treats the
	// last one as the answer.
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
	// EventTurnUsage carries the incremental tokens billed for a completed turn,
	// which core accumulates into per-session lifetime usage totals.
	EventTurnUsage EventKind = "turn_usage"
	// EventNativeAgentActivity carries provider-native subagent/custom-agent
	// lifecycle metadata for non-intrusive UI activity chips.
	EventNativeAgentActivity EventKind = "native_agent_activity"
	// EventToolUse reports one tool invocation observed in the provider stream
	// (a shell command, file edit, install, web fetch, or MCP call). It is audit
	// metadata: core records it on the goal timeline for goal-linked runs, where
	// yolo mode means tool calls never reach the permission broker.
	EventToolUse EventKind = "tool_use"
	// EventPlanProposed carries a plan produced by the provider's own plan mode.
	// The two providers surface it differently — Claude writes a file, Codex
	// emits a typed plan item — and both adapters normalize to this event so
	// core has one capture path. Emitted at most once per turn; a plan turn may
	// legitimately produce none.
	EventPlanProposed EventKind = "plan_proposed"
	// EventRateLimited reports that the active turn cannot continue on this
	// backing target because the provider rate-limited it.
	EventRateLimited EventKind = "rate_limited"
	// EventTurnDone marks the end of a turn stream.
	EventTurnDone EventKind = "turn_done"
)

// ToolUse reports one provider tool invocation observed in the stream. It is
// audit metadata: core truncates Input before persisting or broadcasting it.
// Summary is a best-effort one-liner (the shell command, the file path, the
// search query) computed by the adapter for known tools.
type ToolUse struct {
	Provider  config.Provider `json:"provider"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input,omitempty"`
	Summary   string          `json:"summary,omitempty"`
}

// Event is one streamed provider event.
type Event struct {
	Kind              EventKind
	Content           string
	Handle            *Handle
	PermissionRequest *PermissionRequest
	UserInputRequest  *UserInputRequest
	RateStatus        *RateStatus
	ContextStatus     *ContextStatus
	TurnUsage         *TurnUsage
	NativeAgent       *NativeAgentActivity
	ToolUse           *ToolUse
	PlanProposal      *PlanProposal
}

// PlanProposal is a plan produced by a provider's native plan mode, normalized
// across providers. Markdown is authoritative and always set; FilePath is the
// provider's own artifact when it wrote one (Claude) and empty otherwise
// (Codex), and is informational — Podiom keeps its own canonical copy either
// way, so the provider's file may be overwritten or absent later.
type PlanProposal struct {
	Markdown string
	FilePath string
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

// CredentialRefresher is implemented by adapters that cache ExtraEnv in a
// long-lived provider process. RefreshCredentials asks the adapter to pick up
// newly stored credentials at the next safe point (a turn boundary), without
// aborting an in-flight turn. Per-turn adapters (Claude) re-read credentials on
// every turn and need not implement this — the interface is optional and
// callers type-assert for it.
type CredentialRefresher interface {
	RefreshCredentials()
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
	// EndsTurn distinguishes follow-up questions emitted from a completed
	// provider turn from questions whose answer resumes the same blocked turn.
	// It is deliberately always serialized so clients do not have to infer the
	// behavior from provider identity for newly emitted requests.
	EndsTurn bool `json:"ends_turn"`
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
