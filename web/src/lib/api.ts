// Typed REST helpers for the Podiom daemon. The chat stream uses the WebSocket
// (see Chat.svelte); everything else is plain JSON over these helpers.
// All calls go through request() (lib/http.ts), which resolves paths against
// the app's base (sub-path safety under HA Ingress) and attaches the gateway
// token.

import { request } from "./http";
import type {
  AccessRequest,
  Attachment,
  Agent,
  AgentDetail,
  Dream,
  Goal,
  GoalCreateRequest,
  GoalDetail,
  GoalMemoryRepairResult,
  GoalEvent,
  GoalRunDetail,
  GoalPatchRequest,
  GoalRateLimitBlock,
  AgentQuestion,
  GoalActionItem,
  GoalActionItemStatus,
  CredentialInfo,
  ToolsetEntry,
  ToolsetInstaller,
  WorkspaceFileSnapshot,
  DreamResult,
  GitHubDevicePoll,
  GitHubDeviceStart,
  GitHubRepo,
  GitHubStatus,
  GlobalConfig,
  GlobalConfigPatch,
  Health,
  LogSnapshot,
  LogStreamEvent,
  MCPSnapshot,
  MCPServer,
  MCPTestResult,
  MemoryInfo,
  OnboardingState,
  OnboardingToken,
  GitStatus,
  PermissionMode,
  PlanInfo,
  PlanState,
  ProfileInfo,
  Project,
  ProjectGit,
  ProjectInstructions,
  Provider,
  ProviderAuthStatus,
  ProviderCapabilities,
  ProviderLoginSession,
  ScheduleStatus,
  Session,
  SessionDetail,
  Skill,
  SkillSearchResult,
  SkillDetail,
  SkillSummary,
  SkillFileContent,
  InstalledSkill,
  InstallSkillRequest,
  SkillUpdateStatus,
  Task,
  TaskStatus,
  UpdateApplyResult,
  UpdateStatus,
  UsageSnapshot,
  UserProfileInfo,
  NotificationActionResponse,
  NotificationDevice,
  NotificationDeviceRegistration,
  NotificationListResponse,
  NotificationPreferencesResponse,
  NotificationPreferenceUpdate,
  NotificationStaleResult,
  NotificationTestResult,
  NotificationView,
} from "./types";

async function asJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export async function getHealth(): Promise<Health> {
  return asJSON(await request("/healthz"));
}

export interface CreateWebSessionRequest {
  agent_name: string;
  origin: "web";
  provider?: Provider;
  profile?: string;
  model?: string;
  effort?: string;
  permission_mode?: PermissionMode;
  project_id?: string;
  create_plan_before_implementation?: boolean;
}

export async function createWebSession(body: CreateWebSessionRequest): Promise<Session> {
  return asJSON(
    await request("/api/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

export async function uploadPhotoAttachment(sessionID: string, file: File, visual: Blob): Promise<Attachment> {
  const body = new FormData();
  body.append("file", file, file.name);
  body.append("visual", visual, "visual.jpg");
  return asJSON(
    await request(`/api/sessions/${encodeURIComponent(sessionID)}/attachments`, {
      method: "POST",
      body,
    }),
  );
}

export async function fetchPhotoAttachment(id: string, thumbnail = false): Promise<Blob> {
  const suffix = thumbnail ? "/thumbnail" : "";
  const res = await request(`/api/attachments/${encodeURIComponent(id)}${suffix}`);
  if (!res.ok) throw new Error(await res.text());
  return res.blob();
}

export async function deleteDraftPhotoAttachment(id: string): Promise<void> {
  await asJSON(await request(`/api/attachments/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export class WorkspaceFileRequestError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function getWorkspaceFileSnapshot(id: string): Promise<WorkspaceFileSnapshot> {
  const res = await request(`/api/workspace-files/${encodeURIComponent(id)}`);
  if (!res.ok) {
    const body = (await res.text()).trim();
    throw new WorkspaceFileRequestError(res.status, body || `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as WorkspaceFileSnapshot;
}

export async function getOnboardingState(): Promise<OnboardingState> {
  return asJSON(await request("/api/onboarding"));
}

export async function completeOnboarding(): Promise<OnboardingState> {
  return asJSON(await request("/api/onboarding/complete", { method: "POST" }));
}

export async function getOnboardingToken(): Promise<OnboardingToken> {
  return asJSON(await request("/api/onboarding/token"));
}

export async function checkUpdate(): Promise<UpdateStatus> {
  return asJSON(await request("/api/update"));
}

export async function applyUpdate(force = false): Promise<UpdateApplyResult> {
  return asJSON(
    await request("/api/update/apply", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ force }),
    }),
  );
}

export async function listAgents(): Promise<Agent[]> {
  return (await asJSON<Agent[] | null>(await request("/api/agents"))) ?? [];
}

// getUsage fetches per-profile provider usage snapshots. Live state also arrives
// via the WebSocket `state` frame; this is for on-demand/refresh reads.
export async function getUsage(refresh = false): Promise<UsageSnapshot[]> {
  const path = refresh ? "/api/usage?refresh=1" : "/api/usage";
  return (await asJSON<UsageSnapshot[] | null>(await request(path))) ?? [];
}

export async function listSkills(): Promise<Skill[]> {
  return (await asJSON<Skill[] | null>(await request("/api/skills"))) ?? [];
}

// --- Skill marketplace (Spec 07) ------------------------------------------
// All registry traffic proxies through the daemon (API-2); the frontend never
// talks to SkillsMP/GitHub directly and never sees registry secrets.

export async function searchSkills(q: string, registry = "", page = 1, sort = ""): Promise<SkillSearchResult> {
  const params = new URLSearchParams();
  if (q) params.set("q", q);
  if (registry && registry !== "all") params.set("registry", registry);
  if (sort) params.set("sort", sort);
  params.set("page", String(page));
  return asJSON(await request(`/api/skills/search?${params.toString()}`));
}

export async function skillDetail(registry: string, id: string): Promise<SkillDetail> {
  const params = new URLSearchParams({ registry, id });
  return asJSON(await request(`/api/skills/detail?${params.toString()}`));
}

export async function skillFile(registry: string, id: string, path: string): Promise<SkillFileContent> {
  const params = new URLSearchParams({ registry, id, path });
  return asJSON(await request(`/api/skills/detail/file?${params.toString()}`));
}

export async function resolveSkillURL(url: string): Promise<SkillSummary[]> {
  return (
    (await asJSON<SkillSummary[] | null>(
      await request("/api/skills/resolve", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url }),
      }),
    )) ?? []
  );
}

export async function installSkill(body: InstallSkillRequest): Promise<InstalledSkill> {
  return asJSON(
    await request("/api/skills/install", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

export async function listInstalledSkills(): Promise<InstalledSkill[]> {
  return (await asJSON<InstalledSkill[] | null>(await request("/api/skills/installed"))) ?? [];
}

export async function uninstallSkill(name: string): Promise<void> {
  await asJSON(await request(`/api/skills/installed/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export async function checkSkillUpdate(name: string): Promise<SkillUpdateStatus> {
  return asJSON(await request(`/api/skills/installed/${encodeURIComponent(name)}/update`));
}

export async function applySkillUpdate(name: string, acknowledge: boolean): Promise<InstalledSkill> {
  return asJSON(
    await request(`/api/skills/installed/${encodeURIComponent(name)}/update`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ acknowledge }),
    }),
  );
}

export async function getMCP(): Promise<MCPSnapshot> {
  return asJSON(await request("/api/mcp"));
}

export async function saveMCPServer(server: MCPServer): Promise<MCPSnapshot> {
  return asJSON(
    await request("/api/mcp/servers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(server),
    }),
  );
}

export async function removeMCPServer(name: string): Promise<MCPSnapshot> {
  return asJSON(await request(`/api/mcp/servers/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export async function testMCPServer(name: string): Promise<MCPTestResult> {
  return asJSON(await request(`/api/mcp/servers/${encodeURIComponent(name)}/test`, { method: "POST" }));
}

export async function setMCPAssignment(agentName: string, serverName: string, assigned: boolean): Promise<MCPSnapshot> {
  return asJSON(
    await request("/api/mcp/assignments", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_name: agentName, server_name: serverName, assigned }),
    }),
  );
}

export async function listProfiles(): Promise<ProfileInfo[]> {
  return (await asJSON<ProfileInfo[] | null>(await request("/api/profiles"))) ?? [];
}

export async function getProviderCapabilities(
  provider: Provider,
  profile = "",
  refresh = false,
): Promise<ProviderCapabilities> {
  const params = new URLSearchParams({ provider });
  if (profile) params.set("profile", profile);
  if (refresh) params.set("refresh", "1");
  return asJSON(await request(`/api/provider-capabilities?${params.toString()}`));
}

export interface ProfileRequest {
  name?: string;
  provider?: string;
  config_dir?: string;
  home_dir?: string;
}

export async function createProfile(req: ProfileRequest): Promise<ProfileInfo> {
  return asJSON(
    await request("/api/profiles", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function updateProfile(name: string, req: ProfileRequest): Promise<ProfileInfo> {
  return asJSON(
    await request(`/api/profiles/${encodeURIComponent(name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function deleteProfile(name: string): Promise<void> {
  await asJSON(await request(`/api/profiles/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

// --- Provider sign-in (Settings > profiles) ---
// The daemon runs the provider's own login CLI and reports an authorization URL;
// the browser opens it in a popup and, for Claude, posts back the code the
// redirect page shows. Podiom never sees the resulting token.

// providerStatus reports login state per provider account. The daemon caches the
// fan-out because each row costs a CLI spawn — pass refresh after a sign-in.
export async function providerStatus(refresh = false): Promise<ProviderAuthStatus[]> {
  const path = refresh ? "/api/provider-status?refresh=1" : "/api/provider-status";
  return (await asJSON<ProviderAuthStatus[] | null>(await request(path))) ?? [];
}

export async function startProviderLogin(provider: Provider, profile = ""): Promise<ProviderLoginSession> {
  return asJSON(
    await request("/api/provider-login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ provider, profile }),
    }),
  );
}

export async function pollProviderLogin(id: string): Promise<ProviderLoginSession> {
  return asJSON(await request(`/api/provider-login/${encodeURIComponent(id)}`));
}

export async function submitProviderLoginCode(id: string, code: string): Promise<ProviderLoginSession> {
  return asJSON(
    await request(`/api/provider-login/${encodeURIComponent(id)}/code`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ code }),
    }),
  );
}

export async function cancelProviderLogin(id: string): Promise<void> {
  await request(`/api/provider-login/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function getConfig(): Promise<GlobalConfig> {
  return asJSON(await request("/api/config"));
}

export async function updateConfig(patch: GlobalConfigPatch): Promise<GlobalConfig> {
  return asJSON(
    await request("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

// transcribeAudio uploads a voice recording (raw blob, not multipart) and
// returns the recognized text. The daemon relays it to the OpenAI Whisper API
// server-side so the API key never reaches the browser.
export async function transcribeAudio(blob: Blob): Promise<string> {
  const res = await request("/api/transcribe", {
    method: "POST",
    headers: { "Content-Type": blob.type || "application/octet-stream" },
    body: blob,
  });
  const out = (await asJSON(res)) as { text: string };
  return out.text;
}

export async function getLogs(lines = 200): Promise<LogSnapshot> {
  return asJSON(await request(`/api/logs?lines=${encodeURIComponent(String(lines))}`));
}

export async function followLogs(lines: number, signal: AbortSignal, onEvent: (event: LogStreamEvent) => void): Promise<void> {
  const res = await request(`/api/logs/follow?lines=${encodeURIComponent(String(lines))}`, { signal });
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  if (!res.body) return;
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const parts = buffer.split("\n");
    buffer = parts.pop() ?? "";
    for (const part of parts) {
      const line = part.trim();
      if (!line) continue;
      onEvent(JSON.parse(line) as LogStreamEvent);
    }
  }
  if (buffer.trim()) {
    onEvent(JSON.parse(buffer) as LogStreamEvent);
  }
}

export interface HireRequest {
  name: string;
  provider: string;
  profile?: string;
  model: string;
  effort: string;
  permission_mode: string;
}

export async function hireAgent(req: HireRequest): Promise<Agent> {
  return asJSON(
    await request("/api/agents", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function getAgent(name: string): Promise<AgentDetail> {
  return asJSON(await request(`/api/agents/${encodeURIComponent(name)}`));
}

export interface AgentUpdate {
  provider?: string;
  profile?: string;
  model?: string;
  effort?: string;
  permission_mode?: string;
  fallback?: string[];
  mcp_servers?: string[];
  soul?: string;
}

export async function updateAgent(name: string, patch: AgentUpdate): Promise<AgentDetail> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export interface AgentDeleteResult {
  archive_path?: string;
  archived_sessions: number;
}

export async function deleteAgent(name: string, confirmation: string): Promise<AgentDeleteResult> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ confirmation }),
    }),
  );
}

// --- Agent avatar (profile picture) ---

// fetchAgentAvatar returns the raw image bytes for an agent's uploaded picture.
// /api is token-gated, so the bytes must be fetched through request() (which
// attaches the token) rather than referenced from a plain <img src>. Throws if
// the agent has no picture (404).
export async function fetchAgentAvatar(name: string): Promise<Blob> {
  const res = await request(`/api/agents/${encodeURIComponent(name)}/avatar`);
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.blob();
}

export async function uploadAgentAvatar(name: string, image: Blob): Promise<{ AvatarUpdatedAt: string }> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}/avatar`, {
      method: "POST",
      headers: { "Content-Type": image.type || "image/png" },
      body: image,
    }),
  );
}

export async function deleteAgentAvatar(name: string): Promise<{ AvatarUpdatedAt: string }> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}/avatar`, { method: "DELETE" }),
  );
}

// --- Memory & dreaming ---

export async function getMemory(name: string): Promise<MemoryInfo> {
  return asJSON(await request(`/api/agents/${encodeURIComponent(name)}/memory`));
}

export async function updateMemory(name: string, memory: string): Promise<MemoryInfo> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}/memory`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ memory }),
    }),
  );
}

export async function clearMemory(name: string): Promise<MemoryInfo> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}/memory`, { method: "DELETE" }),
  );
}

export async function getUserProfile(): Promise<UserProfileInfo> {
  return asJSON(await request("/api/user-profile"));
}

export async function saveUserProfile(profile: string): Promise<UserProfileInfo> {
  return asJSON(
    await request("/api/user-profile", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ profile }),
    }),
  );
}

export async function deleteUserProfile(): Promise<void> {
  await asJSON(await request("/api/user-profile", { method: "DELETE" }));
}

export async function dreamAgent(name: string): Promise<DreamResult> {
  return asJSON(
    await request(`/api/agents/${encodeURIComponent(name)}/dream`, { method: "POST" }),
  );
}

export async function listDreams(name: string, limit = 30): Promise<Dream[]> {
  return (
    (await asJSON<Dream[] | null>(
      await request(`/api/agents/${encodeURIComponent(name)}/dreams?limit=${limit}`),
    )) ?? []
  );
}

export async function listSessions(): Promise<Session[]> {
  return (await asJSON<Session[] | null>(await request("/api/sessions"))) ?? [];
}

export async function getSession(id: string): Promise<SessionDetail> {
  return asJSON(await request(`/api/sessions/${id}`));
}

export async function deleteSession(id: string): Promise<void> {
  await asJSON(await request(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export async function setSessionArchived(id: string, archived: boolean): Promise<Session> {
  return asJSON(
    await request(`/api/sessions/${encodeURIComponent(id)}/archive`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ archived }),
    }),
  );
}

export async function listSchedules(): Promise<ScheduleStatus[]> {
  return (await asJSON<ScheduleStatus[] | null>(await request("/api/schedules"))) ?? [];
}

export async function runSchedule(name: string): Promise<unknown> {
  return asJSON(await request(`/api/schedules/${name}/run`, { method: "POST" }));
}

export async function deleteSchedule(name: string): Promise<void> {
  await asJSON(await request(`/api/schedules/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export interface NewScheduleRequest {
  name: string;
  agent: string;
  provider?: Provider | "";
  profile?: string;
  model?: string;
  effort?: string;
  cron?: string;
  every?: string;
  webhook?: boolean;
  run_permission: string;
  allowed_tools?: string[];
  project?: string;
  body: string;
}

export async function createSchedule(req: NewScheduleRequest): Promise<ScheduleStatus> {
  return asJSON(
    await request("/api/schedules", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export interface ScheduleUpdateRequest {
  agent?: string;
  provider?: Provider | "";
  profile?: string;
  model?: string;
  effort?: string;
  cron?: string;
  every?: string;
  webhook?: boolean;
  run_permission?: string;
  enabled?: boolean;
  project?: string;
  body?: string;
}

export async function updateSchedule(name: string, patch: ScheduleUpdateRequest): Promise<ScheduleStatus> {
  return asJSON(
    await request(`/api/schedules/${encodeURIComponent(name)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function listProjects(): Promise<Project[]> {
  return (await asJSON<Project[] | null>(await request("/api/projects"))) ?? [];
}

export interface NewProjectRequest {
  id: string;
  name: string;
  description: string;
  stack: string[];
  notes: string;
  git?: ProjectGit;
}

export async function createProject(req: NewProjectRequest): Promise<Project> {
  return asJSON(
    await request("/api/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export interface ProjectPatch {
  name?: string;
  description?: string;
  color?: string;
  status?: string;
  stack?: string[];
  notes?: string;
  git?: ProjectGit;
}

export async function gitStatus(): Promise<GitStatus> {
  return asJSON(await request("/api/git/status"));
}

export async function setGitIdentity(name: string, email: string): Promise<GitStatus> {
  return asJSON(
    await request("/api/git/identity", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, email }),
    }),
  );
}

export async function updateProject(id: string, patch: ProjectPatch): Promise<Project> {
  return asJSON(
    await request(`/api/projects/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function getProjectInstructions(id: string): Promise<ProjectInstructions> {
  return asJSON(await request(`/api/projects/${encodeURIComponent(id)}/instructions`));
}

export async function updateProjectInstructions(id: string, instructions: string): Promise<ProjectInstructions> {
  return asJSON(
    await request(`/api/projects/${encodeURIComponent(id)}/instructions`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ instructions }),
    }),
  );
}

export interface DeleteProjectResult {
  deleted: string;
  orphaned_tasks: number;
  orphaned_sessions: number;
}

export async function deleteProject(id: string): Promise<DeleteProjectResult> {
  return asJSON(
    await request(`/api/projects/${encodeURIComponent(id)}`, { method: "DELETE" }),
  );
}

export async function describeProject(id: string, agent: string): Promise<string> {
  const res = await asJSON<{ description: string }>(
    await request(`/api/projects/${encodeURIComponent(id)}/describe`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent }),
    }),
  );
  return res.description;
}

export async function githubStatus(): Promise<GitHubStatus> {
  return asJSON(await request("/api/github/status"));
}

export async function githubDeviceStart(): Promise<GitHubDeviceStart> {
  return asJSON(await request("/api/github/device/start", { method: "POST" }));
}

export async function githubDevicePoll(device_code: string): Promise<GitHubDevicePoll> {
  return asJSON(
    await request("/api/github/device/poll", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ device_code }),
    }),
  );
}

export async function githubRepos(): Promise<GitHubRepo[]> {
  return (await asJSON<GitHubRepo[] | null>(await request("/api/github/repos"))) ?? [];
}

export interface ConnectProjectRepoRequest {
  owner: string;
  name: string;
  full_name: string;
  html_url: string;
  clone_url?: string;
  ssh_url?: string;
  default_branch: string;
  ref?: string;
  description?: string;
  force?: boolean;
}

export async function connectProjectRepo(id: string, req: ConnectProjectRepoRequest): Promise<Project> {
  const res = await request(`/api/projects/${encodeURIComponent(id)}/repo`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (res.status === 409) {
    throw new Error("CONFIRM_REPLACE");
  }
  return asJSON(res);
}

export async function createProjectFromGitHub(req: ConnectProjectRepoRequest): Promise<Project> {
  const res = await request("/api/projects/from-github", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (res.status === 409) {
    throw new Error("CONFIRM_REPLACE");
  }
  return asJSON(res);
}

// configureProjectGit is the only call that touches a project's working copy.
// A 409 means the code directory already holds files and the user has not yet
// agreed to have them moved aside; re-send with force once they have.
export async function configureProjectGit(id: string, git: ProjectGit, force = false): Promise<Project> {
  const res = await request(`/api/projects/${encodeURIComponent(id)}/git`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ git, force }),
  });
  if (res.status === 409) {
    throw new Error("CONFIRM_REPLACE");
  }
  return asJSON(res);
}

export async function analyzeProject(id: string, agent?: string): Promise<Project> {
  return asJSON(
    await request(`/api/projects/${encodeURIComponent(id)}/analyze`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent }),
    }),
  );
}

export async function syncProjectRepo(id: string, force = false): Promise<Project> {
  const res = await request(`/api/projects/${encodeURIComponent(id)}/repo/sync`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ force }),
  });
  if (res.status === 409) {
    throw new Error("CONFIRM_REPLACE");
  }
  return asJSON(res);
}

export async function disconnectProjectRepo(id: string): Promise<Project> {
  return asJSON(await request(`/api/projects/${encodeURIComponent(id)}/repo`, { method: "DELETE" }));
}

export interface TaskDescribeRequest {
  id?: string;
  agent?: string;
  project_id?: string;
  title?: string;
  body?: string;
  assigned_agent?: string;
}

export async function describeTask(req: TaskDescribeRequest): Promise<string> {
  const id = req.id?.trim();
  const body = {
    agent: req.agent,
    project_id: req.project_id,
    title: req.title,
    body: req.body,
    assigned_agent: req.assigned_agent,
  };
  const res = await asJSON<{ body: string }>(
    await request(id ? `/api/tasks/${encodeURIComponent(id)}/describe` : "/api/tasks/describe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  return res.body;
}

export async function listTasks(): Promise<Task[]> {
  return (await asJSON<Task[] | null>(await request("/api/tasks"))) ?? [];
}

export interface NewTaskRequest {
  project_id: string;
  title: string;
  body: string;
  assigned_agent: string;
  provider?: Provider | "";
  profile?: string;
  model?: string;
  effort?: string;
  status?: TaskStatus;
  plan_required?: boolean;
  pickup_at?: string;
  goal_id?: string;
}

export async function createTask(req: NewTaskRequest): Promise<Task> {
  return asJSON(
    await request("/api/tasks", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export interface TaskPatch {
  project_id?: string;
  title?: string;
  body?: string;
  assigned_agent?: string;
  provider?: Provider | "";
  profile?: string;
  model?: string;
  effort?: string;
  status?: TaskStatus;
  plan_required?: boolean;
  pickup_at?: string;
  goal_id?: string;
}

export async function updateTask(id: string, patch: TaskPatch): Promise<Task> {
  return asJSON(
    await request(`/api/tasks/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export interface PlanStatus {
  session_id: string;
  state: PlanState;
  explicit: boolean;
  plan: PlanInfo;
}

export interface PlanDecision {
  session: Session;
  next_message?: string;
}

export async function getPlan(sessionId: string): Promise<PlanStatus> {
  return asJSON(await request(`/api/plans/${encodeURIComponent(sessionId)}`));
}

export async function approvePlan(sessionId: string): Promise<PlanDecision> {
  return asJSON(await request(`/api/plans/${encodeURIComponent(sessionId)}/approve`, { method: "POST" }));
}

export async function feedbackPlan(sessionId: string, feedback: string): Promise<PlanDecision> {
  return asJSON(
    await request(`/api/plans/${encodeURIComponent(sessionId)}/feedback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ feedback }),
    }),
  );
}

export async function rejectPlan(sessionId: string): Promise<Session> {
  return asJSON(await request(`/api/plans/${encodeURIComponent(sessionId)}/reject`, { method: "POST" }));
}

export async function deleteTask(id: string): Promise<void> {
  await asJSON(await request(`/api/tasks/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export interface ArchiveDoneResult {
  archive_path?: string;
  archived_tasks: number;
  archived_sessions: number;
}

export async function archiveDoneTasks(): Promise<ArchiveDoneResult> {
  return asJSON(
    await request("/api/tasks/archive-done", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    }),
  );
}

export async function startTask(id: string): Promise<Session> {
  return asJSON(await request(`/api/tasks/${id}/start`, { method: "POST" }));
}

// taskSession returns the latest session for a started task, or null if none.
export async function taskSession(id: string): Promise<Session | null> {
  const res = await request(`/api/tasks/${id}/session`);
  if (res.status === 404) return null;
  return asJSON(res);
}

// getVapidKey returns the daemon's VAPID public key for Web Push subscription.
// An empty key means push is disabled on the daemon.
export async function getVapidKey(): Promise<{ public_key: string }> {
  return asJSON(await request("/api/push/vapid"));
}

// subscribePush registers a browser Web Push subscription with the daemon.
export async function subscribePush(subscription: unknown): Promise<void> {
  await asJSON(
    await request("/api/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(subscription),
    }),
  );
}

// unsubscribePush removes a browser Web Push subscription from the daemon.
export async function unsubscribePush(subscription: unknown): Promise<void> {
  await asJSON(
    await request("/api/push/unsubscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(subscription),
    }),
  );
}

// --- Notifications -----------------------------------------------------------

// NotificationQuery narrows a Notification Center page.
export interface NotificationQuery {
  limit?: number;
  offset?: number;
  unreadOnly?: boolean;
  unresolvedOnly?: boolean;
  category?: string;
}

// listNotifications fetches one page of notification history. The response's
// unread count covers everything, not just the requested filter, so the badge and
// a filtered view cannot disagree.
export async function listNotifications(
  query: NotificationQuery = {},
): Promise<NotificationListResponse> {
  const params = new URLSearchParams();
  if (query.limit !== undefined) params.set("limit", String(query.limit));
  if (query.offset !== undefined) params.set("offset", String(query.offset));
  if (query.unreadOnly) params.set("unread", "1");
  if (query.unresolvedOnly) params.set("unresolved", "1");
  if (query.category) params.set("category", query.category);
  const suffix = params.size > 0 ? `?${params}` : "";
  return asJSON(await request(`/api/notifications${suffix}`));
}

// markNotificationRead marks a notification seen, or returns it to unread.
//
// Reading is not resolving: it never touches the domain object the notification is
// about.
export async function markNotificationRead(
  id: string,
  read = true,
): Promise<NotificationView> {
  return asJSON(
    await request(`/api/notifications/${encodeURIComponent(id)}/${read ? "read" : "unread"}`, {
      method: "POST",
    }),
  );
}

// markAllNotificationsRead clears the unread badge.
export async function markAllNotificationsRead(): Promise<{ updated: number }> {
  return asJSON(await request("/api/notifications/read-all", { method: "POST" }));
}

// resolveNotification records that the user has dealt with a notification without
// performing its underlying operation — dismissing an ask, not answering it.
export async function resolveNotification(id: string): Promise<NotificationView> {
  return asJSON(
    await request(`/api/notifications/${encodeURIComponent(id)}/resolve`, { method: "POST" }),
  );
}

// getNotificationPreferences fetches the whole preference model — categories,
// headings, labels and current settings — so the settings screen hardcodes none of
// it and a newly added notification type appears without a client change.
export async function getNotificationPreferences(): Promise<NotificationPreferencesResponse> {
  return asJSON(await request("/api/notifications/preferences"));
}

// setNotificationPreferences records which events notify the user. The response is
// the updated model, so callers need no follow-up read.
export async function setNotificationPreferences(
  preferences: NotificationPreferenceUpdate[],
): Promise<NotificationPreferencesResponse> {
  return asJSON(
    await request("/api/notifications/preferences", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ preferences }),
    }),
  );
}

// runNotificationAction performs one of a notification's actions.
//
// A 409 is an expected outcome, not a transport failure: the notification may have
// outlived the thing it was about, and the body then carries the actions that are
// still valid plus the resource's current state. It is returned rather than thrown so
// callers have to deal with it.
export async function runNotificationAction(
  id: string,
  actionID: string,
): Promise<NotificationActionResponse> {
  const response = await request(
    `/api/notifications/${encodeURIComponent(id)}/actions/${encodeURIComponent(actionID)}`,
    { method: "POST" },
  );
  if (response.status === 409) {
    return (await response.json()) as NotificationStaleResult;
  }
  return asJSON(response);
}

// registerNotificationDevice registers this device for native push, or refreshes an
// existing registration.
//
// Idempotent on the Podiom device id, so it is safe to call on every launch and on every
// token refresh. The push token goes up and is never returned: anything holding it can
// reach the device.
export async function registerNotificationDevice(body: {
  device_id: string;
  platform: "ios" | "android";
  label?: string;
  push_token: string;
  app_version?: string;
}): Promise<NotificationDeviceRegistration> {
  return asJSON(
    await request("/api/notification-devices", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

// listNotificationDevices returns the devices registered with this installation.
export async function listNotificationDevices(): Promise<{
  devices: NotificationDevice[];
  installation_id: string;
}> {
  return asJSON(await request("/api/notification-devices"));
}

// setNotificationDeviceEnabled mutes or unmutes one device.
//
// This is registration state — whether this phone receives anything — and is separate
// from notification preferences, which decide which events matter.
export async function setNotificationDeviceEnabled(
  id: string,
  enabled: boolean,
): Promise<NotificationDevice> {
  return asJSON(
    await request(
      `/api/notification-devices/${encodeURIComponent(id)}/${enabled ? "enable" : "disable"}`,
      { method: "POST" },
    ),
  );
}

// deleteNotificationDevice removes a registration entirely.
export async function deleteNotificationDevice(id: string): Promise<void> {
  await asJSON(
    await request(`/api/notification-devices/${encodeURIComponent(id)}`, { method: "DELETE" }),
  );
}

// sendTestNotificationPush pushes a real notification to every registered device and
// reports what the relay said about each one.
//
// Bypasses notification preferences on purpose: a muted preference would make the button
// do nothing and say nothing. An empty results array means no device was eligible.
export async function sendTestNotificationPush(): Promise<{
  results: NotificationTestResult[];
}> {
  return asJSON(await request("/api/notification-devices/test", { method: "POST" }));
}

// --- Goals -------------------------------------------------------------------

export async function listGoals(status = ""): Promise<Goal[]> {
  const q = status ? `?status=${encodeURIComponent(status)}` : "";
  return asJSON(await request(`/api/goals${q}`));
}

export async function createGoal(body: GoalCreateRequest): Promise<Goal> {
  return asJSON(
    await request("/api/goals", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

// getGoal returns the goal plus its recent timeline and access requests.
export async function getGoal(id: string): Promise<GoalDetail> {
  return asJSON(await request(`/api/goals/${id}`));
}

export async function getGoalRun(goalId: string, runId: string): Promise<GoalRunDetail> {
  return asJSON(await request(`/api/goals/${encodeURIComponent(goalId)}/runs/${encodeURIComponent(runId)}`));
}

export async function patchGoal(id: string, patch: GoalPatchRequest): Promise<Goal> {
  return asJSON(
    await request(`/api/goals/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function deleteGoal(id: string): Promise<void> {
  await asJSON(await request(`/api/goals/${id}`, { method: "DELETE" }));
}

// listGoalEvents pages the audit timeline: entries older than `before`.
export async function listGoalEvents(id: string, limit = 50, before = 0): Promise<GoalEvent[]> {
  const params = new URLSearchParams();
  if (limit > 0) params.set("limit", String(limit));
  if (before > 0) params.set("before", String(before));
  const q = params.size > 0 ? `?${params.toString()}` : "";
  return asJSON(await request(`/api/goals/${id}/events${q}`));
}

export async function addGoalFeedback(id: string, body: string, pinned = false): Promise<GoalEvent> {
  return asJSON(
    await request(`/api/goals/${id}/feedback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(pinned ? { body, pinned } : { body }),
    }),
  );
}

export async function updateGoalFeedback(id: string, eventId: number, body: string): Promise<GoalEvent> {
  return asJSON(
    await request(`/api/goals/${id}/feedback`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ event_id: eventId, body }),
    }),
  );
}

// setGoalFeedbackPin promotes a feedback note to a standing directive, or
// retires one. Pinning is human-only: no agent tool reaches this.
export async function setGoalFeedbackPin(id: string, eventId: number, pinned: boolean): Promise<GoalEvent> {
  return asJSON(
    await request(`/api/goals/${id}/feedback`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ event_id: eventId, pinned }),
    }),
  );
}

// runGoalReview triggers an unattended review session now; it returns as soon
// as the review is started (results land on the timeline).
export async function runGoalReview(id: string): Promise<{ status: string; goal_id: string }> {
  return asJSON(await request(`/api/goals/${id}/review`, { method: "POST" }));
}

export async function repairGoalMemory(id: string): Promise<GoalMemoryRepairResult> {
  return asJSON(await request(`/api/goals/${encodeURIComponent(id)}/repair-memory`, { method: "POST" }));
}

export async function resolveGoalRateLimit(
  id: string,
  body: { provider?: Provider | ""; profile?: string; model?: string; effort?: string; retry?: boolean },
): Promise<{ status: string; goal: Goal; rate_limit: GoalRateLimitBlock }> {
  return asJSON(
    await request(`/api/goal-rate-limits/${id}/resolve`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

// answerAgentQuestion submits the user's answers to a deferred question an
// unattended agent asked (from a goal or schedule page). answers maps each
// question id to its selected/freeform answers.
export async function answerAgentQuestion(
  id: string,
  answers: Record<string, string[]>,
): Promise<AgentQuestion> {
  return asJSON(
    await request(`/api/agent-questions/${encodeURIComponent(id)}/answer`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ answers }),
    }),
  );
}

// respondGoalActionItem records the user's verdict on a step the agent handed
// them. Stored, not delivered: the agent reads it in its next review, so this
// never starts a run. A verdict can only be given once.
export async function respondGoalActionItem(
  id: string,
  status: GoalActionItemStatus,
  response: string,
): Promise<GoalActionItem> {
  return asJSON(
    await request(`/api/goal-action-items/${encodeURIComponent(id)}/respond`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ status, response }),
    }),
  );
}

export async function listAccessRequests(goalId = "", status = ""): Promise<AccessRequest[]> {
  const params = new URLSearchParams();
  if (goalId) params.set("goal_id", goalId);
  if (status) params.set("status", status);
  const q = params.size > 0 ? `?${params.toString()}` : "";
  return asJSON(await request(`/api/access-requests${q}`));
}

// approveAccessRequest approves and — for automatable kinds — executes the
// grant; the returned request carries the outcome (executed/failed). For
// env_var requests, a non-empty secretValue fulfills the credential: it is
// sent once, stored on the daemon, and never returned by any API.
export async function approveAccessRequest(id: string, note = "", secretValue = ""): Promise<AccessRequest> {
  const body: { note: string; secret_value?: string } = { note };
  if (secretValue) body.secret_value = secretValue;
  return asJSON(
    await request(`/api/access-requests/${id}/approve`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
}

export async function denyAccessRequest(id: string, note = ""): Promise<AccessRequest> {
  return asJSON(
    await request(`/api/access-requests/${id}/deny`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ note }),
    }),
  );
}

// --- Credentials ----------------------------------------------------------------

// listCredentials returns stored credential metadata — names only, never values.
export async function listCredentials(): Promise<CredentialInfo[]> {
  return asJSON(await request("/api/credentials"));
}

// CredentialExistsError is the overwrite guard coming back: the name is already
// in the store and the caller did not ask to replace it. Callers confirm with
// the user and retry with overwrite=true. The daemon's wording is matched in one
// place here rather than at every call site.
export class CredentialExistsError extends Error {
  // Not called `name`: that is Error's own property, and shadowing it would
  // make every thrown instance report the credential where the error kind goes.
  readonly credentialName: string;
  constructor(credentialName: string, message: string) {
    super(message);
    this.name = "CredentialExistsError";
    this.credentialName = credentialName;
  }
}

// storeCredential writes one secret to the daemon's credentials store. The value
// is sent once and is never returned by any API — the response carries metadata
// only. Both writers use this endpoint: this form, and an agent's
// podiom_store_credential. Pass overwrite only after the user has confirmed
// replacing that specific credential.
export async function storeCredential(input: {
  name: string;
  value: string;
  purpose?: string;
  overwrite?: boolean;
}): Promise<CredentialInfo> {
  const res = await request("/api/credentials", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(input),
  });
  if (!res.ok) {
    const body = (await res.text()).trim();
    if (body.includes("overwrite=true")) throw new CredentialExistsError(input.name, body);
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as CredentialInfo;
}

export async function deleteCredential(name: string): Promise<void> {
  await request(`/api/credentials/${encodeURIComponent(name)}`, { method: "DELETE" });
}

// --- Toolset -------------------------------------------------------------------

export async function listToolset(): Promise<ToolsetEntry[]> {
  return asJSON(await request("/api/toolset"));
}

// installToolsetTool runs to completion server-side, so this resolves only once
// the tool is installed and verified — or rejects with why it failed.
export async function installToolsetTool(spec: {
  tool: string;
  installer: ToolsetInstaller;
  package?: string;
  version?: string;
  url?: string;
  sha256?: string;
  path?: string;
}): Promise<void> {
  await asJSON(
    await request("/api/toolset", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(spec),
    }),
  );
}

export async function removeToolsetTool(tool: string): Promise<void> {
  await asJSON(await request(`/api/toolset/${encodeURIComponent(tool)}`, { method: "DELETE" }));
}
