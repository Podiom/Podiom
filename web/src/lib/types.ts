// Shared TypeScript types mirroring the server data model and WebSocket
// protocol.

export interface Health {
  status: string;
  version: string;
  commit: string;
  started: string;
  uptime_ms: number;
}

export interface UpdateStatus {
  current_version: string;
  current_commit: string;
  latest_version: string;
  latest_commitish: string;
  update_available: boolean;
  asset_name: string;
  asset_url: string;
  checksum_url: string;
  release_url: string;
  release_notes: string;
  blocking_reason?: string;
}

export interface UpdateApplyResult {
  status: UpdateStatus;
  installed: boolean;
  helper_started: boolean;
  restart_required: boolean;
  message: string;
}

export interface LogSnapshot {
  path: string;
  lines: string[];
}

export interface LogStreamEvent {
  type: "line" | "reopen";
  line?: string;
}

export type Provider = "claude" | "codex";
export type PermissionMode = "approve" | "yolo";

// GlobalConfig mirrors GET /api/config: the daemon-wide defaults every new
// agent and ad-hoc run inherits unless overridden. `fallback` is the ordered
// re-route chain (profile names, bare providers, or "default").
export interface GlobalConfig {
  provider: Provider;
  profile: string;
  model: string;
  effort: string;
  permission_mode: PermissionMode;
  permission_timeout: string;
  fallback: string[];
}

// GlobalConfigPatch is the PATCH /api/config body. Omitted fields keep their
// current value; a present-but-empty fallback clears the chain.
export interface GlobalConfigPatch {
  provider?: Provider;
  profile?: string;
  model?: string;
  effort?: string;
  permission_mode?: PermissionMode;
  permission_timeout?: string;
  fallback?: string[];
}
export type SessionOrigin = "web" | "cli" | "onboarding" | "schedule" | "roadmap";
export type MessageRole = "user" | "assistant";

export interface Agent {
  Name: string;
  Provider: Provider;
  Profile: string;
  Model: string;
  Effort: string;
  PermissionMode: PermissionMode;
  // Ordered fallback chain. Each entry is a profile name, a bare provider
  // ("claude"/"codex", no profile), or "default" (the agent's own provider).
  Fallback: string[];
  MCPServers?: string[];
}

// ProfileInfo mirrors the GET /api/profiles response: configured auth profiles
// as name + provider, with no directory/credential detail.
export interface ProfileInfo {
  Name: string;
  Provider: Provider;
  ConfigDir?: string;
  HomeDir?: string;
}

export interface Session {
  ID: string;
  AgentName: string;
  Name: string;
  Description: string;
  AutoNamed: boolean;
  Provider: Provider;
  Profile: string;
  Model: string;
  Effort: string;
  PermissionMode: PermissionMode;
  Origin: SessionOrigin;
  ScheduleID: string;
  RunID: string;
  TaskID: string;
  ProjectID: string;
  ProviderHandle: string;
}

export type TaskStatus = "backlog" | "in_progress" | "review" | "done";

export interface Project {
  id: string;
  name: string;
  description: string;
  color: string;
  path: string;
  status: string;
  stack: string[];
  repo: ProjectRepo | null;
  roadmap: string[];
  notes: string;
}

export interface ProjectRepo {
  provider: string;
  mode: string;
  owner: string;
  name: string;
  full_name: string;
  html_url: string;
  default_branch: string;
  ref: string;
  synced_at: string;
  source_kind: string;
}

export interface GitHubStatus {
  configured: boolean;
  authed: boolean;
  app_slug: string;
  client_id?: string;
  install_url?: string;
  message?: string;
}

export interface GitHubDeviceStart {
  device_code: string;
  user_code: string;
  verification_uri: string;
  expires_in: number;
  interval: number;
}

export interface GitHubDevicePoll {
  status: string;
  error?: string;
}

export interface GitHubRepo {
  id: number;
  owner: string;
  name: string;
  full_name: string;
  html_url: string;
  default_branch: string;
  description: string;
  private: boolean;
}

// AgentDetail is the GET /api/agents/<name> response: durable defaults plus the
// editable SOUL.md body.
export interface AgentDetail extends Agent {
  Soul: string;
}

export type MCPSource = "podium" | "claude" | "codex";
export type MCPTransport = "http" | "stdio";

export interface MCPEnvStatus {
  name: string;
  set: boolean;
}

export interface MCPServer {
  name: string;
  transport: MCPTransport;
  url?: string;
  command?: string;
  args?: string[];
  env_vars?: string[];
  sources?: MCPSource[];
  env_status?: MCPEnvStatus[];
}

export interface MCPAgent {
  name: string;
  provider: Provider;
  mcp_servers: string[];
}

export interface MCPSnapshot {
  servers: MCPServer[];
  agents: MCPAgent[];
  assignments: Record<string, string[]>;
}

// Task mirrors store.Task (Go-exported field names, no json tags).
export interface Task {
  ID: string;
  ProjectID: string;
  Title: string;
  Body: string;
  AssignedAgent: string;
  Status: TaskStatus;
  PickupAt: string;
  CreatedAt: string;
  UpdatedAt: string;
}

// SessionDetail is the GET /api/sessions/<id> response, including roadmap
// provenance when the session was started from a task.
export interface SessionDetail {
  session: Session;
  history: Message[];
  task?: Task;
  project_id?: string;
  project_name?: string;
}

export type RunPermission = "preapproved" | "yolo";
export type RunTrigger = "cron" | "manual";
export type RunStatus = "running" | "success" | "error";

// ScheduleRun mirrors store.ScheduleRun (Go-exported field names, no json tags).
export interface ScheduleRun {
  ID: string;
  ScheduleName: string;
  SessionID: string;
  Trigger: RunTrigger;
  Status: RunStatus;
  Error: string;
  StartedAt: string;
  FinishedAt: string;
}

// ScheduleStatus mirrors schedule.Status (json-tagged, snake_case).
export interface ScheduleStatus {
  name: string;
  path: string;
  agent: string;
  model: string;
  effort: string;
  cron: string;
  every: string;
  run_permission: RunPermission;
  allowed_tools: string[];
  enabled: boolean;
  next_run?: string;
  parse_error?: string;
  runs: ScheduleRun[];
}

export interface Message {
  ID: number;
  SessionID: string;
  Seq: number;
  Role: MessageRole;
  Content: string;
}

export interface PermissionRequest {
  id: string;
  turn_id: string;
  tool_name: string;
  tool_use_id: string;
  description?: string;
  input: Record<string, unknown>;
  expires_at?: string;
}

export interface PermissionDecision {
  behavior: "allow" | "deny";
  updatedInput?: Record<string, unknown>;
  message?: string;
}

export interface UserInputOption {
  label: string;
  description?: string;
}

export interface UserInputQuestion {
  id: string;
  header?: string;
  question: string;
  options?: UserInputOption[];
  multi_select?: boolean;
  is_other?: boolean;
  is_secret?: boolean;
}

export interface UserInputRequest {
  id: string;
  turn_id?: string;
  provider?: Provider;
  item_id?: string;
  questions: UserInputQuestion[];
  auto_resolution_ms?: number;
}

export interface UserInputDecision {
  answers: Record<string, string[]>;
}

export interface ActiveTurnSummary {
  session_id: string;
  turn_id: string;
  status: "running" | "done" | "error" | "stopped";
  pending?: "permission" | "question" | "assistant" | "";
}

// Usage mirrors internal/usage.Snapshot (snake_case JSON). Per-profile provider
// plan-limit utilization surfaced in the composer usage chip.
export type UsageStatus =
  | "ok"
  | "no_credentials"
  | "stale_credentials"
  | "unauthorized"
  | "rate_limited"
  | "unsupported"
  | "error";

export interface UsageWindow {
  key: string;
  label: string;
  used_percent: number;
  resets_at?: string;
  window_seconds?: number;
}

export interface UsageCredits {
  enabled: boolean;
  unlimited?: boolean;
  balance?: number;
  monthly_limit?: number;
  used_credits?: number;
  utilization_percent?: number;
  currency?: string;
}

export interface UsageSnapshot {
  profile: string;
  provider: Provider;
  default: boolean;
  plan?: string;
  status: UsageStatus;
  error?: string;
  windows?: UsageWindow[];
  credits?: UsageCredits;
  fetched_at?: string;
  next_retry_at?: string;
  source?: string;
}

export interface TurnState {
  session_id: string;
  turn_id: string;
  status: "running" | "done" | "error" | "stopped";
  pending_assistant?: string;
  pending_permission?: PermissionRequest;
  pending_user_input?: UserInputRequest;
  error?: string;
}

export type ClientMessage =
  | { type: "list"; request_id?: string }
  | { type: "attach_session"; request_id?: string; session_id: string }
  | { type: "stop_turn"; request_id?: string; session_id: string }
  | {
      type: "update_session_settings";
      request_id: string;
      session_id: string;
      model?: string;
      effort?: string;
      permission_mode?: PermissionMode;
    }
  | {
      type: "create_session";
      request_id: string;
      agent_name: string;
      model?: string;
      effort?: string;
      permission_mode?: PermissionMode;
      project_id?: string;
    }
  | {
      type: "send_turn";
      request_id: string;
      agent_name?: string;
      session_id?: string;
      message: string;
      model?: string;
      effort?: string;
      permission_mode?: PermissionMode;
      project_id?: string;
    }
  | { type: "permission_decision"; request_id: string; decision: PermissionDecision }
  | { type: "user_input_decision"; request_id: string; input: UserInputDecision };

export interface ServerMessage {
  type:
    | "hello"
    | "state"
    | "session"
    | "history"
    | "message"
    | "delta"
    | "assistant"
    | "permission_request"
    | "user_input_request"
    | "turn_state"
    | "notice"
    | "done"
    | "error";
  request_id?: string;
  session_id?: string;
  agents?: Agent[];
  sessions?: Session[];
  active_turns?: ActiveTurnSummary[];
  usage?: UsageSnapshot[];
  session?: Session;
  history?: Message[];
  message?: Message;
  delta?: string;
  notice?: string;
  request?: PermissionRequest;
  input?: UserInputRequest;
  turn_state?: TurnState;
  error?: string;
}

// Skills catalogue (read-only). Mirrors internal/skills.Skill. `agents` is the
// shared union (~/.agents/skills); `claude`/`codex` are the providers' own dirs.
export type SkillSource = "agents" | "claude" | "codex";

export interface SkillLocation {
  source: SkillSource;
  path: string;
}

export interface SkillContent {
  source?: SkillSource; // set per-source only when the skill is in conflict
  body: string;
}

export interface Skill {
  name: string;
  description: string;
  sources: SkillSource[];
  conflict: boolean;
  locations: SkillLocation[];
  contents: SkillContent[];
}
