// Shared TypeScript types mirroring the server data model and WebSocket
// protocol.

export interface Health {
  status: string;
  version: string;
  commit: string;
  started: string;
  uptime_ms: number;
}

export interface OnboardingState {
  completed: boolean;
  completed_at?: string;
}

export interface OnboardingToken {
  token: string;
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

export interface EffortOption {
  effort: string;
  description?: string;
}

export interface ModelOption {
  id?: string;
  model: string;
  display_name?: string;
  description?: string;
  hidden?: boolean;
  is_default?: boolean;
  default_reasoning_effort?: string;
  supported_efforts?: EffortOption[];
}

export interface ProviderCapabilities {
  provider: Provider;
  profile?: string;
  source: string;
  fetched_at: string;
  stale: boolean;
  error?: string;
  models: ModelOption[];
  efforts: EffortOption[];
}

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
  voice: VoiceConfig;
}

// VoiceConfig mirrors the `voice:` config block. The OpenAI key is a secret
// that never leaves the daemon — reads only expose whether one is set.
export interface VoiceConfig {
  openai_api_key_set: boolean;
}

// GlobalConfigPatch is the PATCH /api/config body. Omitted fields keep their
// current value; a present-but-empty fallback clears the chain, and a
// present-but-empty voice.openai_api_key clears the stored key.
export interface GlobalConfigPatch {
  provider?: Provider;
  profile?: string;
  model?: string;
  effort?: string;
  permission_mode?: PermissionMode;
  permission_timeout?: string;
  fallback?: string[];
  voice?: { openai_api_key?: string };
}
export type SessionOrigin = "web" | "cli" | "onboarding" | "schedule" | "roadmap" | "goal";
export type MessageRole = "user" | "assistant";
export type MessageKind = "message" | "error" | "reasoning";
export type PlanState = "none" | "pending_submission" | "awaiting_approval";

export interface PlanInfo {
  file_path: string;
  markdown: string;
  submitted_at: string;
  updated_at: string;
}

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
  // Version stamp of the agent's uploaded profile picture (empty/absent = none).
  // Changes whenever a new picture is uploaded, so the client cache-busts.
  AvatarUpdatedAt?: string;
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
  GoalID: string;
  ProjectID: string;
  ProviderHandle: string;
  PlanState: PlanState;
  PlanExplicit: boolean;
  PlanInfo: PlanInfo;
  // Set once a session has been consolidated into the agent's memory. Empty
  // means the session is un-dreamed (pending consolidation).
  DreamedAt?: string;
  // Context-window utilization persisted per session: last request's prompt size
  // and the model's window. 0 means un-observed. Drives the composer context ring.
  ContextTokens: number;
  ContextLimit: number;
  // Cumulative billed-token totals accumulated over the session's lifetime. The
  // percentage share of the 5-hour/weekly limits is computed server-side and
  // delivered as a UsageEstimate (session detail + live "session_usage" message).
  Usage?: SessionUsageTotals;
}

// SessionUsageTotals mirrors store.SessionUsage: raw cumulative token counts.
export interface SessionUsageTotals {
  InputTokens: number;
  OutputTokens: number;
  CacheReadTokens: number;
  CacheWriteTokens: number;
}

// UsageEstimate mirrors tokenmeter.Estimate: a token total expressed as an
// estimated share of the provider's 5-hour and weekly plan limits. The percentages
// are calibrated approximations (the provider exposes no absolute token ceiling),
// so the UI presents them as estimates (~) rather than exact figures.
export interface UsageEstimate {
  tokens: number;
  five_hour_percent: number;
  weekly_percent: number;
  calibrated: boolean;
}

// ContextUsage mirrors server.ContextUsage: live context-window utilization for a
// session pushed over the WebSocket during a turn.
export interface ContextUsage {
  used: number;
  max: number;
}

// DreamTrigger and DreamStatus mirror store.Dream* enums.
export type DreamTrigger = "nightly" | "manual";
export type DreamStatus = "running" | "success" | "error";

// DreamNewItem is a memory bullet a dream added, used for NEW badges and dates.
export interface DreamNewItem {
  section: string;
  text: string;
}

// Dream mirrors store.Dream (Go-exported field names): one memory-consolidation
// run, doubling as a "dream journal" entry.
export interface Dream {
  ID: string;
  AgentName: string;
  RanAt: string;
  FinishedAt: string;
  Trigger: DreamTrigger;
  Status: DreamStatus;
  Error: string;
  SessionCount: number;
  Kept: number;
  Merged: number;
  Pruned: number;
  Note: string;
  NewItems: DreamNewItem[] | null;
}

// MemoryInfo mirrors GET/PUT /api/agents/<name>/memory.
export interface MemoryInfo {
  agent: string;
  memory: string;
  lines: number;
  budget_lines: number;
  pending_sessions: number;
  last_dream: Dream | null;
}

// DreamResult mirrors POST /api/agents/<name>/dream.
export interface DreamResult {
  noop: boolean;
  dream: Dream | null;
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
  instructions: string;
}

export interface ProjectInstructions {
  project_id: string;
  path: string;
  instructions: string;
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

export type MCPSource = "podiom" | "claude" | "codex";
export type MCPTransport = "http" | "stdio";

export interface MCPEnvStatus {
  name: string;
  set: boolean;
}

export interface MCPEnvVar {
  name: string;
  // Value is optional: omit/blank it to pass the var through from the
  // daemon's own OS environment instead of storing a value in Podiom.
  value?: string;
}

export interface MCPServer {
  name: string;
  transport: MCPTransport;
  url?: string;
  command?: string;
  args?: string[];
  env_vars?: MCPEnvVar[];
  sources?: MCPSource[];
  env_status?: MCPEnvStatus[];
}

export interface MCPTestStep {
  name: string;
  status: "ok" | "error" | string;
  detail?: string;
  duration_ms: number;
}

export interface MCPTestResult {
  server: string;
  transport: MCPTransport;
  ok: boolean;
  duration_ms: number;
  steps: MCPTestStep[];
  logs: string[];
  error?: string;
  tool_count: number;
  stderr_tail?: string;
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
  Provider: Provider | "";
  Profile: string;
  Model: string;
  Effort: string;
  Status: TaskStatus;
  PlanRequired: boolean;
  PickupAt: string;
  GoalID: string;
  CreatedAt: string;
  UpdatedAt: string;
}

// --- Goals -----------------------------------------------------------------

export type GoalStatus = "active" | "paused" | "review" | "done" | "abandoned";

// GoalMetric mirrors store.GoalMetric (explicit json tags, lowercase).
export interface GoalMetric {
  name: string;
  target: number;
  current: number;
  unit?: string;
}

// Goal mirrors store.Goal (Go-exported field names, no json tags).
export interface Goal {
  ID: string;
  Title: string;
  Description: string;
  SuccessCriteria: string;
  Metrics: GoalMetric[];
  ReviewEvery: string;
  LeadAgent: string;
  ProjectID: string;
  Provider: Provider | "";
  Profile: string;
  Model: string;
  Effort: string;
  LeadSessionID: string;
  Status: GoalStatus;
  NextReviewAt: string;
  ClosingReport: string;
  CreatedAt: string;
  UpdatedAt: string;
  // Rolled-up token usage across the goal's sessions, as an estimated share of the
  // 5-hour/weekly limits. Present on the list response; absent when unmeasured.
  Usage?: UsageEstimate;
  pending_rate_limit?: GoalRateLimitBlock;
  pending_question?: AgentQuestion;
}

export type GoalEventKind =
  | "created"
  | "planning_started"
  | "review_started"
  | "progress"
  | "metric_update"
  | "plan_change"
  | "user_feedback"
  | "access_requested"
  | "access_decided"
  | "status_change"
  | "completion_proposed"
  | "rate_limited"
  | "rate_limit_resolved"
  | "tool_use"
  | "question_asked"
  | "question_answered";

// GoalEvent mirrors store.GoalEvent: one append-only audit timeline entry.
export interface GoalEvent {
  ID: number;
  GoalID: string;
  SessionID: string;
  RunID: string;
  Kind: GoalEventKind;
  Body: string;
  Payload: string;
  CreatedAt: string;
}

export type AccessRequestKind = "mcp_server" | "skill" | "cli_tool" | "env_var" | "permission_mode";
export type AccessRequestStatus = "pending" | "approved" | "denied" | "executed" | "failed";

// AccessRequest mirrors store.AccessRequest.
export interface AccessRequest {
  ID: string;
  GoalID: string;
  AgentName: string;
  SessionID: string;
  Kind: AccessRequestKind;
  Payload: string;
  Reason: string;
  Status: AccessRequestStatus;
  DecisionNote: string;
  ExecutionError: string;
  CreatedAt: string;
  DecidedAt: string;
  ExecutedAt: string;
}

export type GoalRateLimitStatus = "pending" | "resolved";
export type GoalRateLimitPhase = "planning" | "review";

export interface GoalRateLimitBlock {
  ID: string;
  GoalID: string;
  SessionID: string;
  RunID: string;
  Phase: GoalRateLimitPhase;
  Provider: Provider;
  Profile: string;
  Model: string;
  Effort: string;
  Error: string;
  Status: GoalRateLimitStatus;
  ResolvedProvider: Provider | "";
  ResolvedProfile: string;
  ResolvedModel: string;
  ResolvedEffort: string;
  CreatedAt: string;
  ResolvedAt: string;
}

export type GoalRunKind = "planning" | "review" | "task" | "schedule" | "conversation";
export type GoalRunStatus = "running" | "succeeded" | "failed" | "rate_limited" | "interrupted";

export interface GoalRun {
  ID: string;
  GoalID: string;
  SessionID: string;
  TurnMessageID: number;
  Kind: GoalRunKind;
  AgentName: string;
  SourceID: string;
  Status: GoalRunStatus;
  Legacy: boolean;
  Error: string;
  StartedAt: string;
  FinishedAt: string;
}

export interface GoalRunDetail {
  run: GoalRun;
  session?: Session;
  messages: Message[];
  events: GoalEvent[];
  transcript_available: boolean;
}

// GoalDetail is the GET /api/goals/<id> response.
export interface GoalDetail {
  goal: Goal;
  events: GoalEvent[];
  access_requests: AccessRequest[];
  rate_limit_blocks: GoalRateLimitBlock[];
  pending_question?: AgentQuestion;
  usage?: UsageEstimate;
}

export interface GoalCreateRequest {
  title: string;
  description: string;
  success_criteria: string;
  metrics: GoalMetric[];
  review_every: string;
  lead_agent: string;
  project_id: string;
  provider?: Provider | "";
  profile?: string;
  model?: string;
  effort?: string;
}

export interface GoalPatchRequest {
  title?: string;
  description?: string;
  success_criteria?: string;
  metrics?: GoalMetric[];
  review_every?: string;
  lead_agent?: string;
  project_id?: string;
  status?: GoalStatus;
  status_note?: string;
}

// WorkspaceTool mirrors tools.ToolStatus: one workspace-installed tool with
// its manifest provenance and live on-disk health.
export interface WorkspaceTool {
  tool: string;
  installer: "npm" | "uv" | "go" | "binary";
  package?: string;
  version?: string;
  url?: string;
  sha256?: string;
  request_id?: string;
  goal_id?: string;
  installed_at: string;
  version_output?: string;
  broken: boolean;
}

// SessionDetail is the GET /api/sessions/<id> response, including roadmap
// provenance when the session was started from a task.
export interface SessionDetail {
  session: Session;
  history: Message[];
  task?: Task;
  project_id?: string;
  project_name?: string;
  usage?: UsageEstimate;
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
  provider: Provider | "";
  profile: string;
  model: string;
  effort: string;
  cron: string;
  every: string;
  run_permission: RunPermission;
  allowed_tools: string[];
  enabled: boolean;
  goal_id?: string;
  body: string;
  next_run?: string;
  parse_error?: string;
  runs: ScheduleRun[];
  // Present when a run of this schedule asked the user a question (via
  // podiom_ask_user) that is still awaiting an answer.
  pending_question?: AgentQuestion;
}

export interface Message {
  ID: number;
  SessionID: string;
  Seq: number;
  Role: MessageRole;
  Kind?: MessageKind;
  Content: string;
  CreatedAt?: string;
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

// AgentQuestion mirrors store.AgentQuestion: a question an unattended agent (a
// goal or scheduled run) asked the user, recorded to be answered from the goal
// or schedule page. Questions reuse the chat UserInputQuestion shape.
export interface AgentQuestion {
  ID: string;
  Origin: "goal" | "schedule";
  RefID: string;
  SessionID: string;
  Questions: UserInputQuestion[];
  Status: "pending" | "answered" | "dismissed";
  Answers: Record<string, string[]>;
  CreatedAt: string;
  AnsweredAt: string;
}

export interface UserInputDecision {
  answers: Record<string, string[]>;
}

// CredentialInfo is the value-free projection of a stored credential
// (GET /api/credentials). The secret value never leaves the daemon.
export interface CredentialInfo {
  name: string;
  purpose?: string;
  goal_id?: string;
  created_at?: string;
}

export interface ActiveTurnSummary {
  session_id: string;
  turn_id: string;
  status: "running" | "done" | "error" | "stopped";
  pending?: "permission" | "question" | "fallback" | "assistant" | "";
}

// FallbackTarget is one selectable provider/profile in a session-limit prompt.
export interface FallbackTarget {
  provider: Provider;
  profile: string;
  label: string;
}

// FallbackRequest is surfaced when a session hits a provider rate limit and the
// user must choose how to continue. Switching recreates the conversation history
// on the new provider/profile.
export interface FallbackRequest {
  id: string;
  turn_id: string;
  session_id: string;
  provider: Provider;
  profile: string;
  label: string;
  next_label?: string;
  has_fallback: boolean;
  targets: FallbackTarget[];
  expires_at?: string;
}

export interface FallbackDecision {
  action: "use_configured" | "switch";
  provider?: Provider;
  profile?: string;
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
  pending_reasoning?: string;
  pending_assistant?: string;
  pending_permission?: PermissionRequest;
  pending_user_input?: UserInputRequest;
  pending_fallback?: FallbackRequest;
  native_agent_activities?: NativeAgentActivity[];
  error?: string;
}

export interface NativeAgentActivity {
  provider: Provider;
  task_id?: string;
  tool_use_id?: string;
  provider_agent_name?: string;
  podiom_agent_name?: string;
  display_name?: string;
  description?: string;
  status?: "started" | "completed" | "failed" | "cancelled" | "canceled" | string;
}

// UserProfileInfo mirrors the /api/user-profile view of the app-wide USER.md.
export interface UserProfileInfo {
  exists: boolean;
  profile: string;
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
      provider?: Provider;
      profile?: string;
      model?: string;
      effort?: string;
      permission_mode?: PermissionMode;
      project_id?: string;
      create_plan_before_implementation?: boolean;
    }
  | {
      type: "send_turn";
      request_id: string;
      agent_name?: string;
      session_id?: string;
      message: string;
      provider?: Provider;
      profile?: string;
      model?: string;
      effort?: string;
      permission_mode?: PermissionMode;
      project_id?: string;
      create_plan_before_implementation?: boolean;
    }
  | {
      type: "start_interview";
      request_id: string;
      agent_name: string;
      provider?: Provider;
      profile?: string;
      model?: string;
      effort?: string;
    }
  | { type: "plan_approve"; request_id: string; session_id: string }
  | { type: "plan_feedback"; request_id: string; session_id: string; feedback: string }
  | { type: "plan_reject"; request_id: string; session_id: string }
  | { type: "permission_decision"; request_id: string; decision: PermissionDecision }
  | { type: "user_input_decision"; request_id: string; input: UserInputDecision }
  | { type: "fallback_decision"; request_id: string; fallback_decision: FallbackDecision }
  | { type: "dream"; request_id: string; agent_name: string };

export interface ServerMessage {
  type:
    | "hello"
    | "state"
    | "session"
    | "history"
    | "message"
    | "reasoning_delta"
    | "reasoning"
    | "delta"
    | "assistant"
    | "permission_request"
    | "user_input_request"
    | "fallback_request"
    | "native_agent_activity"
    | "turn_state"
    | "context"
    | "session_usage"
    | "notice"
    | "done"
    | "dream_state"
    | "goal_event"
    | "error";
  request_id?: string;
  session_id?: string;
  agents?: Agent[];
  sessions?: Session[];
  active_turns?: ActiveTurnSummary[];
  usage?: UsageSnapshot[];
  session?: Session;
  plan?: PlanInfo;
  next_message?: string;
  history?: Message[];
  message?: Message;
  delta?: string;
  notice?: string;
  request?: PermissionRequest;
  input?: UserInputRequest;
  fallback?: FallbackRequest;
  native_agent?: NativeAgentActivity;
  turn_state?: TurnState;
  context?: ContextUsage;
  // session_usage: the session's updated token-usage estimate, pushed after a turn.
  session_usage?: UsageEstimate;
  error?: string;
  // dream_state fields.
  agent_name?: string;
  dream_phase?: DreamPhase;
  dream?: Dream;
  // goal_event: one appended goal-timeline entry, broadcast to every client.
  goal_event?: GoalEvent;
}

// DreamPhase is the lifecycle a manual dream streams over the WebSocket so the
// dream-sequence overlay can animate.
export type DreamPhase =
  | "gathering"
  | "distilling"
  | "integrating"
  | "done"
  | "noop"
  | "error";

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

// Skill marketplace (Spec 07). A `SkillRegistry` is a search source (a registry),
// distinct from `SkillSource` above which is a local on-disk root. Mirrors the Go
// DTOs in internal/marketplace.
export type SkillRegistry = "skillsmp" | "anthropics" | "github";

export interface SkillRef {
  owner: string;
  repo: string;
  path: string;
  sha?: string;
}

export interface SkillSummary {
  id: string;
  registry: SkillRegistry;
  name: string;
  description: string;
  owner: string;
  ref: SkillRef;
  stars?: number;
  installs?: number;
  updated_at?: string;
  has_scripts: boolean;
  verified: boolean;
  installed: boolean;
  update_available: boolean;
}

export interface FrontmatterField {
  key: string;
  value: string;
}

export interface FileNode {
  path: string;
  is_dir: boolean;
  size: number;
  executable: boolean;
}

export interface ScanFinding {
  file: string;
  rule: string;
  severity: string; // "info" | "warn"
  message: string;
}

export interface SkillDetail extends SkillSummary {
  frontmatter: FrontmatterField[];
  skill_md: string;
  tree: FileNode[];
  license?: string;
  has_executable: boolean;
  size: number;
  scan_findings: ScanFinding[];
}

export interface SkillSearchResult {
  results: SkillSummary[];
  warnings: string[];
}

export interface SkillFileContent {
  path: string;
  content: string;
  binary: boolean;
}

export interface InstalledSkill {
  name: string;
  description: string;
  managed: boolean;
  registry?: SkillRegistry;
  owner?: string;
  repo?: string;
  path?: string;
  sha?: string;
  installed_at?: string;
  repo_url?: string;
  roots: string[];
  update_available: boolean;
}

export interface SkillUpdateStatus {
  name: string;
  available: boolean;
  current_sha: string;
  latest_sha: string;
  changed?: string[];
  installed: boolean;
}

export interface InstallSkillRequest {
  registry?: SkillRegistry;
  id?: string;
  url?: string;
  acknowledge?: boolean;
}
