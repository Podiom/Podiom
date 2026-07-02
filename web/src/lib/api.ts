// Typed REST helpers for the Podiom daemon. The chat stream uses the WebSocket
// (see Chat.svelte); everything else is plain JSON over these helpers.

import type {
  Agent,
  AgentDetail,
  Dream,
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
  MemoryInfo,
  ProfileInfo,
  Project,
  Provider,
  ProviderCapabilities,
  ScheduleStatus,
  Session,
  SessionDetail,
  Skill,
  Task,
  TaskStatus,
  UpdateApplyResult,
  UpdateStatus,
  UsageSnapshot,
} from "./types";

async function asJSON<T>(res: Response): Promise<T> {
  if (!res.ok) {
    const body = await res.text();
    throw new Error(body || `${res.status} ${res.statusText}`);
  }
  return (await res.json()) as T;
}

export async function getHealth(): Promise<Health> {
  return asJSON(await fetch("/healthz"));
}

export async function checkUpdate(): Promise<UpdateStatus> {
  return asJSON(await fetch("/api/update"));
}

export async function applyUpdate(force = false): Promise<UpdateApplyResult> {
  return asJSON(
    await fetch("/api/update/apply", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ force }),
    }),
  );
}

export async function listAgents(): Promise<Agent[]> {
  return (await asJSON<Agent[] | null>(await fetch("/api/agents"))) ?? [];
}

// getUsage fetches per-profile provider usage snapshots. Live state also arrives
// via the WebSocket `state` frame; this is for on-demand/refresh reads.
export async function getUsage(refresh = false): Promise<UsageSnapshot[]> {
  const path = refresh ? "/api/usage?refresh=1" : "/api/usage";
  return (await asJSON<UsageSnapshot[] | null>(await fetch(path))) ?? [];
}

export async function listSkills(): Promise<Skill[]> {
  return (await asJSON<Skill[] | null>(await fetch("/api/skills"))) ?? [];
}

export async function getMCP(): Promise<MCPSnapshot> {
  return asJSON(await fetch("/api/mcp"));
}

export async function saveMCPServer(server: MCPServer): Promise<MCPSnapshot> {
  return asJSON(
    await fetch("/api/mcp/servers", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(server),
    }),
  );
}

export async function removeMCPServer(name: string): Promise<MCPSnapshot> {
  return asJSON(await fetch(`/api/mcp/servers/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export async function setMCPAssignment(agentName: string, serverName: string, assigned: boolean): Promise<MCPSnapshot> {
  return asJSON(
    await fetch("/api/mcp/assignments", {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent_name: agentName, server_name: serverName, assigned }),
    }),
  );
}

export async function listProfiles(): Promise<ProfileInfo[]> {
  return (await asJSON<ProfileInfo[] | null>(await fetch("/api/profiles"))) ?? [];
}

export async function getProviderCapabilities(
  provider: Provider,
  profile = "",
  refresh = false,
): Promise<ProviderCapabilities> {
  const params = new URLSearchParams({ provider });
  if (profile) params.set("profile", profile);
  if (refresh) params.set("refresh", "1");
  return asJSON(await fetch(`/api/provider-capabilities?${params.toString()}`));
}

export interface ProfileRequest {
  name?: string;
  provider?: string;
  config_dir?: string;
  home_dir?: string;
}

export async function createProfile(req: ProfileRequest): Promise<ProfileInfo> {
  return asJSON(
    await fetch("/api/profiles", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function updateProfile(name: string, req: ProfileRequest): Promise<ProfileInfo> {
  return asJSON(
    await fetch(`/api/profiles/${encodeURIComponent(name)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function deleteProfile(name: string): Promise<void> {
  await asJSON(await fetch(`/api/profiles/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export async function getConfig(): Promise<GlobalConfig> {
  return asJSON(await fetch("/api/config"));
}

export async function updateConfig(patch: GlobalConfigPatch): Promise<GlobalConfig> {
  return asJSON(
    await fetch("/api/config", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function getLogs(lines = 200): Promise<LogSnapshot> {
  return asJSON(await fetch(`/api/logs?lines=${encodeURIComponent(String(lines))}`));
}

export async function followLogs(lines: number, signal: AbortSignal, onEvent: (event: LogStreamEvent) => void): Promise<void> {
  const res = await fetch(`/api/logs/follow?lines=${encodeURIComponent(String(lines))}`, { signal });
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
    await fetch("/api/agents", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function getAgent(name: string): Promise<AgentDetail> {
  return asJSON(await fetch(`/api/agents/${encodeURIComponent(name)}`));
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
    await fetch(`/api/agents/${encodeURIComponent(name)}`, {
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
    await fetch(`/api/agents/${encodeURIComponent(name)}`, {
      method: "DELETE",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ confirmation }),
    }),
  );
}

// --- Memory & dreaming ---

export async function getMemory(name: string): Promise<MemoryInfo> {
  return asJSON(await fetch(`/api/agents/${encodeURIComponent(name)}/memory`));
}

export async function updateMemory(name: string, memory: string): Promise<MemoryInfo> {
  return asJSON(
    await fetch(`/api/agents/${encodeURIComponent(name)}/memory`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ memory }),
    }),
  );
}

export async function clearMemory(name: string): Promise<MemoryInfo> {
  return asJSON(
    await fetch(`/api/agents/${encodeURIComponent(name)}/memory`, { method: "DELETE" }),
  );
}

export async function dreamAgent(name: string): Promise<DreamResult> {
  return asJSON(
    await fetch(`/api/agents/${encodeURIComponent(name)}/dream`, { method: "POST" }),
  );
}

export async function listDreams(name: string, limit = 30): Promise<Dream[]> {
  return (
    (await asJSON<Dream[] | null>(
      await fetch(`/api/agents/${encodeURIComponent(name)}/dreams?limit=${limit}`),
    )) ?? []
  );
}

export async function listSessions(): Promise<Session[]> {
  return (await asJSON<Session[] | null>(await fetch("/api/sessions"))) ?? [];
}

export async function getSession(id: string): Promise<SessionDetail> {
  return asJSON(await fetch(`/api/sessions/${id}`));
}

export async function deleteSession(id: string): Promise<void> {
  await asJSON(await fetch(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export async function listSchedules(): Promise<ScheduleStatus[]> {
  return (await asJSON<ScheduleStatus[] | null>(await fetch("/api/schedules"))) ?? [];
}

export async function runSchedule(name: string): Promise<unknown> {
  return asJSON(await fetch(`/api/schedules/${name}/run`, { method: "POST" }));
}

export async function deleteSchedule(name: string): Promise<void> {
  await asJSON(await fetch(`/api/schedules/${encodeURIComponent(name)}`, { method: "DELETE" }));
}

export interface NewScheduleRequest {
  name: string;
  agent: string;
  model?: string;
  effort?: string;
  cron?: string;
  every?: string;
  run_permission: string;
  allowed_tools?: string[];
  body: string;
}

export async function createSchedule(req: NewScheduleRequest): Promise<ScheduleStatus> {
  return asJSON(
    await fetch("/api/schedules", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(req),
    }),
  );
}

export async function listProjects(): Promise<Project[]> {
  return (await asJSON<Project[] | null>(await fetch("/api/projects"))) ?? [];
}

export interface NewProjectRequest {
  id: string;
  name: string;
  description: string;
  stack: string[];
  notes: string;
}

export async function createProject(req: NewProjectRequest): Promise<Project> {
  return asJSON(
    await fetch("/api/projects", {
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
}

export async function updateProject(id: string, patch: ProjectPatch): Promise<Project> {
  return asJSON(
    await fetch(`/api/projects/${encodeURIComponent(id)}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function describeProject(id: string, agent: string): Promise<string> {
  const res = await asJSON<{ description: string }>(
    await fetch(`/api/projects/${encodeURIComponent(id)}/describe`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent }),
    }),
  );
  return res.description;
}

export async function githubStatus(): Promise<GitHubStatus> {
  return asJSON(await fetch("/api/github/status"));
}

export async function githubDeviceStart(): Promise<GitHubDeviceStart> {
  return asJSON(await fetch("/api/github/device/start", { method: "POST" }));
}

export async function githubDevicePoll(device_code: string): Promise<GitHubDevicePoll> {
  return asJSON(
    await fetch("/api/github/device/poll", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ device_code }),
    }),
  );
}

export async function githubRepos(): Promise<GitHubRepo[]> {
  return (await asJSON<GitHubRepo[] | null>(await fetch("/api/github/repos"))) ?? [];
}

export interface ConnectProjectRepoRequest {
  owner: string;
  name: string;
  full_name: string;
  html_url: string;
  default_branch: string;
  ref?: string;
  description?: string;
  force?: boolean;
}

export async function connectProjectRepo(id: string, req: ConnectProjectRepoRequest): Promise<Project> {
  const res = await fetch(`/api/projects/${encodeURIComponent(id)}/repo`, {
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
  const res = await fetch("/api/projects/from-github", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(req),
  });
  if (res.status === 409) {
    throw new Error("CONFIRM_REPLACE");
  }
  return asJSON(res);
}

export async function analyzeProject(id: string, agent?: string): Promise<Project> {
  return asJSON(
    await fetch(`/api/projects/${encodeURIComponent(id)}/analyze`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ agent }),
    }),
  );
}

export async function syncProjectRepo(id: string, force = false): Promise<Project> {
  const res = await fetch(`/api/projects/${encodeURIComponent(id)}/repo/sync`, {
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
  return asJSON(await fetch(`/api/projects/${encodeURIComponent(id)}/repo`, { method: "DELETE" }));
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
    await fetch(id ? `/api/tasks/${encodeURIComponent(id)}/describe` : "/api/tasks/describe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    }),
  );
  return res.body;
}

export async function listTasks(): Promise<Task[]> {
  return (await asJSON<Task[] | null>(await fetch("/api/tasks"))) ?? [];
}

export interface NewTaskRequest {
  project_id: string;
  title: string;
  body: string;
  assigned_agent: string;
  status?: TaskStatus;
  pickup_at?: string;
}

export async function createTask(req: NewTaskRequest): Promise<Task> {
  return asJSON(
    await fetch("/api/tasks", {
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
  status?: TaskStatus;
  pickup_at?: string;
}

export async function updateTask(id: string, patch: TaskPatch): Promise<Task> {
  return asJSON(
    await fetch(`/api/tasks/${id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(patch),
    }),
  );
}

export async function deleteTask(id: string): Promise<void> {
  await asJSON(await fetch(`/api/tasks/${encodeURIComponent(id)}`, { method: "DELETE" }));
}

export interface ArchiveDoneResult {
  archive_path?: string;
  archived_tasks: number;
  archived_sessions: number;
}

export async function archiveDoneTasks(): Promise<ArchiveDoneResult> {
  return asJSON(
    await fetch("/api/tasks/archive-done", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({}),
    }),
  );
}

export async function startTask(id: string): Promise<Session> {
  return asJSON(await fetch(`/api/tasks/${id}/start`, { method: "POST" }));
}

// taskSession returns the latest session for a started task, or null if none.
export async function taskSession(id: string): Promise<Session | null> {
  const res = await fetch(`/api/tasks/${id}/session`);
  if (res.status === 404) return null;
  return asJSON(res);
}

// getVapidKey returns the daemon's VAPID public key for Web Push subscription.
// An empty key means push is disabled on the daemon.
export async function getVapidKey(): Promise<{ public_key: string }> {
  return asJSON(await fetch("/api/push/vapid"));
}

// subscribePush registers a browser Web Push subscription with the daemon.
export async function subscribePush(subscription: unknown): Promise<void> {
  await asJSON(
    await fetch("/api/push/subscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(subscription),
    }),
  );
}

// unsubscribePush removes a browser Web Push subscription from the daemon.
export async function unsubscribePush(subscription: unknown): Promise<void> {
  await asJSON(
    await fetch("/api/push/unsubscribe", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(subscription),
    }),
  );
}
