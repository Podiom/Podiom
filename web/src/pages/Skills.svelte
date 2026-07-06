<script lang="ts">
  import { onMount } from "svelte";
  import { getMCP, removeMCPServer, saveMCPServer, setMCPAssignment, testMCPServer } from "../lib/api";
  import type { MCPAgent, MCPSnapshot, MCPServer, MCPSource, MCPTestResult, SkillDetail, SkillSummary } from "../lib/types";
  import ConfirmModal from "../lib/ConfirmModal.svelte";
  import Discover from "./skills/Discover.svelte";
  import Installed from "./skills/Installed.svelte";
  import SkillDetailView from "./skills/SkillDetail.svelte";
  import InstallModal from "./skills/InstallModal.svelte";
  import GitHubUrlModal from "./skills/GitHubUrlModal.svelte";

  let mcp = $state<MCPSnapshot>({ servers: [], agents: [], assignments: {} });
  let loadError = $state<string | null>(null);

  let tab = $state<"skills" | "mcp">("skills");
  let subTab = $state<"discover" | "installed">("discover");

  // Marketplace navigation + modal state.
  let detailTarget = $state<{ registry: string; id: string } | null>(null);
  let installTarget = $state<InstallTargetShape | null>(null);
  let githubOpen = $state(false);
  let toast = $state<string | null>(null);
  // Bumping this key remounts the Installed list so it reloads after an install.
  let installedKey = $state(0);

  interface InstallTargetShape {
    name: string;
    registry?: string;
    id?: string;
    url?: string;
    hasScripts: boolean;
    sha?: string;
    size?: number;
  }

  let mcpOpen = $state<Record<string, boolean>>({});
  let addOpen = $state(false);
  let addName = $state("");
  let addTransport = $state<"http" | "stdio">("stdio");
  let addEndpoint = $state("");
  let addCommand = $state("");
  let addArgs = $state<string[]>([]);
  let addEnvVars = $state("");
  let savingServer = $state(false);
  // When set, the add form is editing this existing (podiom-owned) server rather
  // than creating a new one; the name field is locked so save upserts in place.
  let editing = $state<string | null>(null);
  let removeTarget = $state<MCPServer | null>(null);
  let removingServer = $state(false);
  let testingMCP = $state<Record<string, boolean>>({});
  let mcpTests = $state<Record<string, MCPTestResult>>({});

  // A server is user-managed (editable/removable) when it lives in Podiom's own
  // MCP catalogue; claude/codex-only servers are imported and read-only here.
  function isUserServer(server: MCPServer): boolean {
    return (server.sources ?? []).includes("podiom");
  }

  onMount(async () => {
    try {
      mcp = await getMCP();
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });

  function openDetail(registry: string, id: string) {
    detailTarget = { registry, id };
  }

  function installFromSummary(s: SkillSummary) {
    installTarget = {
      name: s.name,
      registry: s.registry,
      id: s.id,
      hasScripts: s.has_scripts,
      sha: s.ref.sha,
    };
  }

  function installFromDetail(d: SkillDetail) {
    installTarget = {
      name: d.name,
      registry: d.registry,
      id: d.id,
      hasScripts: d.has_executable,
      sha: d.ref.sha,
      size: d.size,
    };
  }

  function onInstalled(name: string) {
    installTarget = null;
    detailTarget = null;
    toast = `Installed ${name} into ~/.agents/skills/`;
    installedKey += 1;
    setTimeout(() => (toast = null), 4000);
  }

  // --- MCP (unchanged) ------------------------------------------------------
  const MCP_SRC: Record<MCPSource, { label: string; fg: string; bg: string; bd: string; glyph: string }> = {
    podiom: { label: "podiom", fg: "#2F6E60", bg: "#E7F0EC", bd: "#C7DBD2", glyph: "circle" },
    claude: { label: "claude", fg: "#B0572F", bg: "#F8EBE2", bd: "#ECD3C2", glyph: "square" },
    codex: { label: "codex", fg: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2", glyph: "diamond" },
  };

  function dot(color: string, glyph = "circle"): string {
    const shape = glyph === "circle" ? "border-radius:99px" : glyph === "square" ? "border-radius:2px" : "border-radius:1px;transform:rotate(45deg)";
    return `width:8px;height:8px;background:${color};flex:none;${shape}`;
  }
  function mcpChip(src: MCPSource): string {
    const s = MCP_SRC[src];
    return `display:inline-flex;align-items:center;gap:6px;padding:4px 9px;border-radius:8px;font:600 11px 'JetBrains Mono',monospace;color:${s.fg};background:${s.bg};border:1px solid ${s.bd};white-space:nowrap`;
  }

  function assigned(agent: MCPAgent, server: MCPServer): boolean {
    return (mcp.assignments[agent.name] ?? agent.mcp_servers ?? []).includes(server.name);
  }
  function codexStatus(server: MCPServer, isAssigned: boolean): string {
    if (!isAssigned) return "off";
    if (server.transport === "stdio") return "native";
    return "bridged";
  }
  function cellStyle(state: string): string {
    const base = "width:36px;height:36px;border-radius:10px;border:1px solid;display:flex;align-items:center;justify-content:center;cursor:pointer;font:800 14px 'Hanken Grotesk';";
    if (state === "native") return base + "background:#E7F0EC;border-color:#BFE0D6;color:#2F6E60";
    if (state === "bridged") return base + "background:#FBF1DD;border-color:#ECD8A6;color:#9A6B1A";
    return base + "background:#fff;border-color:#EAE0D4;color:#C7B49B";
  }
  async function toggleAssignment(agent: MCPAgent, server: MCPServer) {
    try {
      mcp = await setMCPAssignment(agent.name, server.name, !assigned(agent, server));
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  }
  async function addServer() {
    savingServer = true;
    loadError = null;
    try {
      const env_vars = addEnvVars.split(/[,\s]+/).map((s) => s.trim()).filter(Boolean);
      const server: MCPServer = { name: addName.trim(), transport: addTransport, env_vars };
      if (addTransport === "http") {
        server.url = addEndpoint.trim();
      } else {
        server.command = addCommand.trim();
        server.args = addArgs.map((a) => a.trim()).filter(Boolean);
      }
      mcp = await saveMCPServer(server);
      resetServerForm();
      addOpen = false;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      savingServer = false;
    }
  }
  function resetServerForm() {
    editing = null;
    addName = "";
    addEndpoint = "";
    addCommand = "";
    addArgs = [];
    addEnvVars = "";
    addTransport = "stdio";
  }
  function toggleAdd() {
    if (addOpen) {
      addOpen = false;
      return;
    }
    resetServerForm();
    addOpen = true;
  }
  function startEdit(server: MCPServer) {
    editing = server.name;
    addName = server.name;
    addTransport = server.transport === "http" ? "http" : "stdio";
    addEndpoint = server.url ?? "";
    addCommand = server.command ?? "";
    addArgs = [...(server.args ?? [])];
    addEnvVars = (server.env_vars ?? []).join(", ");
    addOpen = true;
    mcpOpen = { ...mcpOpen, [server.name]: false };
  }
  async function removeServer() {
    if (!removeTarget) return;
    removingServer = true;
    loadError = null;
    try {
      mcp = await removeMCPServer(removeTarget.name);
      removeTarget = null;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      removingServer = false;
    }
  }
  // Each arg is a separate value, edited in its own field so users can add/remove
  // one at a time. mcp-proxy and friends expect one token per arg (never a whole
  // command line packed into a single arg).
  function addArg() {
    addArgs = [...addArgs, ""];
  }
  function removeArg(index: number) {
    addArgs = addArgs.filter((_, i) => i !== index);
  }
  function updateArg(index: number, value: string) {
    addArgs = addArgs.map((a, i) => (i === index ? value : a));
  }
  function canSaveServer(): boolean {
    if (!addName.trim()) return false;
    if (addTransport === "http") return Boolean(addEndpoint.trim());
    return Boolean(addCommand.trim());
  }
  async function runMCPTest(server: MCPServer) {
    testingMCP = { ...testingMCP, [server.name]: true };
    loadError = null;
    try {
      const result = await testMCPServer(server.name);
      mcpTests = { ...mcpTests, [server.name]: result };
      mcpOpen = { ...mcpOpen, [server.name]: true };
    } catch (e) {
      const message = e instanceof Error ? e.message : String(e);
      mcpTests = {
        ...mcpTests,
        [server.name]: {
          server: server.name,
          transport: server.transport,
          ok: false,
          duration_ms: 0,
          steps: [],
          logs: [message],
          error: message,
          tool_count: 0,
        },
      };
    } finally {
      testingMCP = { ...testingMCP, [server.name]: false };
    }
  }
  function testTitle(result: MCPTestResult | undefined): string {
    if (!result) return "Not tested";
    if (result.ok) return `OK · ${result.tool_count} tools · ${result.duration_ms}ms`;
    return `Failed · ${result.duration_ms}ms`;
  }
  function envText(server: MCPServer): string {
    const envs = server.env_status ?? [];
    if (!envs.length) return "no env vars";
    return envs.map((e) => `${e.name} ${e.set ? "set" : "unset"}`).join(", ");
  }
  function definition(server: MCPServer): string {
    const lines = [`- name: ${server.name}`, `  transport: ${server.transport}`];
    if (server.transport === "http") lines.push(`  url: ${server.url ?? ""}`);
    else {
      lines.push(`  command: ${server.command ?? ""}`);
      if (server.args?.length) lines.push(`  args: [${server.args.map((a) => JSON.stringify(a)).join(", ")}]`);
    }
    if (server.env_vars?.length) lines.push(`  env_vars: [${server.env_vars.join(", ")}]`);
    return lines.join("\n");
  }
</script>

<div class="page">
  <div class="inner">
    <header class="head">
      <div>
        <h1>Skills & MCPs</h1>
        <p>Skills are shared across every agent. MCP servers are assigned per agent and follow that agent across providers.</p>
      </div>
    </header>

    <div class="tabs">
      <button class:active={tab === "skills"} onclick={() => (tab = "skills")}>Skills</button>
      <button class:active={tab === "mcp"} onclick={() => (tab = "mcp")}>MCP servers <span>{mcp.servers.length}</span></button>
    </div>

    {#if loadError}
      <div class="error">Could not load: {loadError}</div>
    {/if}

    {#if tab === "skills"}
      {#if detailTarget}
        <div class="mk">
          <SkillDetailView
            registry={detailTarget.registry}
            id={detailTarget.id}
            onback={() => (detailTarget = null)}
            oninstall={installFromDetail}
          />
        </div>
      {:else}
        <div class="subtabs">
          <button class:active={subTab === "discover"} onclick={() => (subTab = "discover")}>Discover</button>
          <button class:active={subTab === "installed"} onclick={() => (subTab = "installed")}>Installed</button>
        </div>
        <div class="mk">
          {#if subTab === "discover"}
            <Discover onopen={openDetail} oninstall={installFromSummary} onopengithub={() => (githubOpen = true)} />
          {:else}
            {#key installedKey}
              <Installed />
            {/key}
          {/if}
        </div>
      {/if}
    {:else}
      <section class="note">MCP servers are assigned per agent. A cell grants or revokes a server for that agent.</section>

      <section class="mcp-card">
        <div class="mcp-head">
          <div><b>Assignment matrix</b><p>Each assigned set is projected into Claude and Codex at launch.</p></div>
          <button class="primary" onclick={toggleAdd}>+ Add server</button>
        </div>

        {#if addOpen}
          <div class="add-box">
            {#if editing}<div class="add-editing">Editing <b>{editing}</b></div>{/if}
            <input bind:value={addName} placeholder="server name" readonly={editing !== null} />
            <div class="transport">
              <button class:active={addTransport === "stdio"} onclick={() => (addTransport = "stdio")}>stdio</button>
              <button class:active={addTransport === "http"} onclick={() => (addTransport = "http")}>http</button>
            </div>
            {#if addTransport === "http"}
              <input class="wide" bind:value={addEndpoint} placeholder="https://..." />
            {:else}
              <input class="wide" bind:value={addCommand} placeholder="/opt/homebrew/bin/mcp-proxy" />
              <div class="args-list">
                {#each addArgs as arg, i (i)}
                  <div class="arg-row">
                    <input
                      value={arg}
                      oninput={(e) => updateArg(i, e.currentTarget.value)}
                      placeholder={i === 0 ? "--transport" : "arg"}
                    />
                    <button type="button" class="arg-remove" title="Remove argument" onclick={() => removeArg(i)}>×</button>
                  </div>
                {/each}
                <button type="button" class="arg-add" onclick={addArg}>+ Add argument</button>
              </div>
            {/if}
            <input bind:value={addEnvVars} placeholder="ENV_NAMES comma separated" />
            <button class="primary" disabled={savingServer || !canSaveServer()} onclick={addServer}>
              {savingServer ? "Saving..." : editing ? "Save changes" : "Save"}
            </button>
          </div>
        {/if}

        <div class="matrix">
          <div class="matrix-row matrix-top">
            <div class="server-col">server</div>
            {#each mcp.agents as agent (agent.name)}
              <div class="agent-col"><b>{agent.name}</b><span>{agent.provider}</span></div>
            {/each}
          </div>
          {#each mcp.servers as server (server.name)}
            <div class="matrix-wrap">
              <div class="matrix-row">
                <button class="server-cell" onclick={() => (mcpOpen = { ...mcpOpen, [server.name]: !mcpOpen[server.name] })}>
                  <span class:rot={mcpOpen[server.name]} class="chev">›</span>
                  <div>
                    <b>{server.name}</b>
                    <p>
                      {#each server.sources ?? [] as src (src)}
                        <span style={mcpChip(src)}><span style={dot(MCP_SRC[src].fg, MCP_SRC[src].glyph)}></span>{MCP_SRC[src].label}</span>
                      {/each}
                      <span class="transport-chip">{server.transport}</span>
                      <span class="env-chip">{envText(server)}</span>
                    </p>
                  </div>
                </button>
                {#each mcp.agents as agent (agent.name)}
                  {@const isOn = assigned(agent, server)}
                  {@const status = codexStatus(server, isOn)}
                  <div class="agent-col">
                    <button title={`${server.name} for ${agent.name}: ${status}`} style={cellStyle(status)} onclick={() => toggleAssignment(agent, server)}>{isOn ? "✓" : ""}</button>
                  </div>
                {/each}
              </div>
              {#if mcpOpen[server.name]}
                <div class="mcp-detail">
                  <pre>{definition(server)}</pre>
                  <div>
                    <div class="detail-head">
                      <b>Projection</b>
                      <div class="detail-actions">
                        {#if isUserServer(server)}
                          <button class="secondary" onclick={() => startEdit(server)}>Edit</button>
                          <button class="secondary danger" onclick={() => (removeTarget = server)}>Remove</button>
                        {/if}
                        <button class="secondary" disabled={testingMCP[server.name]} onclick={() => runMCPTest(server)}>
                          {testingMCP[server.name] ? "Testing..." : "Test"}
                        </button>
                      </div>
                    </div>
                    <p>Claude: strict --mcp-config. Codex: generated profile with unassigned known servers disabled{server.transport === "http" ? ", HTTP bridged through mcp-proxy when present" : ""}.</p>
                    {#if mcpTests[server.name]}
                      {@const result = mcpTests[server.name]}
                      <div class:ok={Boolean(result.ok)} class:bad={Boolean(!result.ok)} class="test-box">
                        <div class="test-title">{testTitle(result)}</div>
                        <div class="steps">
                          {#each result.steps as step}
                            <div class="step">
                              <span>{step.status === "ok" ? "✓" : "!"}</span>
                              <b>{step.name}</b>
                              <em>{step.detail || `${step.duration_ms}ms`}</em>
                            </div>
                          {/each}
                        </div>
                        {#if result.error}
                          <pre class="test-log">{result.error}</pre>
                        {/if}
                        {#if result.stderr_tail}
                          <pre class="test-log">{result.stderr_tail}</pre>
                        {/if}
                        {#if result.logs.length}
                          <pre class="test-log">{result.logs.join("\n")}</pre>
                        {/if}
                      </div>
                    {:else}
                      <div class="test-box">
                        <div class="test-title">{testTitle(undefined)}</div>
                      </div>
                    {/if}
                  </div>
                </div>
              {/if}
            </div>
          {/each}
        </div>
      </section>
    {/if}
  </div>
</div>

{#if installTarget}
  <InstallModal target={installTarget} onclose={() => (installTarget = null)} oninstalled={(s) => onInstalled(s.name)} />
{/if}
{#if githubOpen}
  <GitHubUrlModal
    onclose={() => (githubOpen = false)}
    onpick={(s) => {
      githubOpen = false;
      openDetail(s.registry, s.id);
    }}
  />
{/if}
{#if removeTarget}
  <ConfirmModal
    title="Remove MCP server?"
    message={`Removes “${removeTarget.name}” from Podiom's MCP catalogue and unassigns it from all agents. Servers imported from Claude or Codex are not affected.`}
    confirmLabel="Remove server"
    busy={removingServer}
    onConfirm={removeServer}
    onCancel={() => (removeTarget = null)}
  />
{/if}
{#if toast}
  <div class="toast">{toast}</div>
{/if}

<style>
  .page { flex: 1; overflow-y: auto; padding: 24px 28px 0; min-height: 0; background: #f4ece2; color: #2b2520; }
  .inner { max-width: 1080px; margin: 0 auto; }
  .head h1 { margin: 0; font: 800 24px "Hanken Grotesk"; letter-spacing: -0.02em; }
  .head p, .note { margin: 4px 0 0; font: 400 13px/1.55 "Hanken Grotesk"; color: #8a7f73; max-width: 650px; }
  .tabs { display: inline-flex; gap: 4px; padding: 4px; margin-top: 18px; border-radius: 13px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .tabs button, .transport button { border: 0; background: transparent; color: #8a7f73; cursor: pointer; border-radius: 9px; padding: 7px 12px; font: 700 12.5px "Hanken Grotesk"; }
  .tabs button.active, .transport button.active { background: #fffdfb; color: #2b2520; box-shadow: 0 1px 3px rgba(43, 37, 32, 0.12); }
  .tabs span { margin-left: 6px; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; }
  .subtabs { display: inline-flex; gap: 4px; padding: 4px; margin-top: 18px; border-radius: 12px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .subtabs button { border: 0; background: transparent; color: #8a7f73; cursor: pointer; border-radius: 9px; padding: 7px 14px; font: 700 12.5px "Hanken Grotesk"; }
  .subtabs button.active { background: #fffdfb; color: #2b2520; box-shadow: 0 1px 3px rgba(43, 37, 32, 0.12); }
  .mk { margin-top: 18px; }
  .error { margin-top: 16px; padding: 12px 14px; border-radius: 12px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 13px "Hanken Grotesk"; }
  .mcp-card { margin-top: 18px; background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; box-shadow: 0 1px 2px rgba(43, 37, 32, 0.04), 0 18px 44px -32px rgba(43, 37, 32, 0.22); overflow: hidden; }
  .mcp-head { display: flex; gap: 14px; align-items: center; justify-content: space-between; padding: 16px 20px; border-bottom: 1px solid #f1eae0; }
  .mcp-head b { font: 800 15px "Hanken Grotesk"; }
  .mcp-head p { margin: 2px 0 0; font: 400 11.5px "Hanken Grotesk"; color: #8a7f73; }
  .primary { border: 0; border-radius: 11px; background: #3f8f7e; color: #fff; padding: 9px 14px; font: 800 13px "Hanken Grotesk"; cursor: pointer; }
  .primary:disabled { opacity: 0.55; cursor: default; }
  .secondary { border: 1px solid #d8cab9; border-radius: 10px; background: #fffdfb; color: #4f6f68; padding: 7px 11px; font: 800 12px "Hanken Grotesk"; cursor: pointer; }
  .secondary:disabled { opacity: 0.55; cursor: default; }
  .secondary.danger { color: #b4472f; border-color: #e6cabd; }
  .secondary.danger:hover { border-color: #d9663d; background: #fdf2ee; }
  .add-box { display: flex; gap: 10px; flex-wrap: wrap; padding: 14px 20px; background: #fbf7f1; border-bottom: 1px solid #f1eae0; }
  .add-editing { flex-basis: 100%; font: 600 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .add-box input[readonly] { background: #f1eae0; color: #7a6f62; cursor: not-allowed; }
  input { padding: 10px 12px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; font: 500 13px "Hanken Grotesk"; color: #2b2520; outline: none; }
  .add-box .wide { flex: 1; min-width: 260px; }
  .args-list { flex: 1 1 100%; display: flex; flex-direction: column; gap: 6px; }
  .arg-row { display: flex; gap: 6px; align-items: center; }
  .arg-row input { flex: 1; font: 500 12px "JetBrains Mono", monospace; }
  .arg-remove { flex: none; width: 34px; height: 34px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; color: #b4472f; font-size: 18px; line-height: 1; cursor: pointer; }
  .arg-remove:hover { border-color: #d9663d; background: #fdf2ee; }
  .arg-add { align-self: flex-start; padding: 7px 12px; border: 1px dashed #d9cdba; border-radius: 11px; background: transparent; color: #7a6f62; font: 600 12px "JetBrains Mono", monospace; cursor: pointer; }
  .arg-add:hover { border-color: #c8a878; color: #2b2520; }
  .transport { display: flex; gap: 4px; padding: 4px; border-radius: 11px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .matrix { overflow-x: auto; padding: 6px 20px 14px; }
  .matrix-row { display: flex; align-items: center; gap: 6px; min-width: max-content; border-bottom: 1px solid #f5eee4; }
  .matrix-top { padding: 14px 0 12px; color: #b7ac9e; font: 600 10px "JetBrains Mono", monospace; text-transform: uppercase; }
  .server-col, .server-cell { width: 300px; flex: none; }
  .server-cell { padding: 12px 0; border: 0; background: transparent; text-align: left; cursor: pointer; display: flex; gap: 12px; align-items: flex-start; }
  .server-cell b { font: 700 15px "JetBrains Mono", monospace; color: #241f1a; }
  .server-cell p { margin: 7px 0 0; display: flex; gap: 7px; flex-wrap: wrap; }
  .chev { display: inline-flex; width: 18px; height: 18px; align-items: center; justify-content: center; color: #b7ac9e; font-size: 22px; transition: transform 0.16s ease; }
  .chev.rot { transform: rotate(90deg); }
  .agent-col { width: 92px; flex: none; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; }
  .agent-col b { font: 700 12px "Hanken Grotesk"; color: #3a332c; text-transform: none; }
  .agent-col span { font: 600 10px "JetBrains Mono", monospace; color: #8a7f73; text-transform: none; }
  .transport-chip, .env-chip { padding: 4px 9px; border-radius: 8px; font: 600 11px "JetBrains Mono", monospace; }
  .transport-chip { background: #efe7dc; border: 1px solid #e6dbcc; color: #8a7560; }
  .env-chip { background: #fbf7f1; border: 1px solid #efe6db; color: #7a6f62; }
  .mcp-detail { border-top: 1px solid #f1eae0; padding: 16px 22px 20px; display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  .mcp-detail b { font: 700 13px "Hanken Grotesk"; }
  .mcp-detail p { margin: 6px 0 0; font: 400 12.5px/1.55 "Hanken Grotesk"; color: #7a6f62; }
  .detail-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; }
  .detail-actions { display: flex; align-items: center; gap: 8px; }
  .test-box { margin-top: 12px; border: 1px solid #efe6db; border-radius: 12px; background: #fbf7f1; padding: 12px; }
  .test-box.ok { border-color: #bfddd3; background: #eef7f3; }
  .test-box.bad { border-color: #ecd3c2; background: #f8ebe2; }
  .test-title { font: 800 12px "Hanken Grotesk"; color: #3a332c; }
  .steps { display: grid; gap: 6px; margin-top: 10px; }
  .step { display: grid; grid-template-columns: 18px minmax(92px, max-content) 1fr; gap: 7px; align-items: baseline; font: 600 11.5px "Hanken Grotesk"; color: #4a4138; }
  .step span { font: 800 12px "JetBrains Mono", monospace; color: #2f6e60; }
  .step em { min-width: 0; font: 500 11px/1.45 "JetBrains Mono", monospace; color: #7a6f62; word-break: break-word; }
  pre { margin: 0; background: #fbf7f1; border: 1px solid #efe6db; border-radius: 12px; padding: 14px 16px; font: 500 12px/1.65 "JetBrains Mono", monospace; color: #4a4138; white-space: pre-wrap; word-break: break-word; }
  .test-log { margin-top: 10px; padding: 10px 12px; font-size: 11px; line-height: 1.5; max-height: 180px; overflow: auto; background: rgba(255, 253, 251, 0.78); }
  .toast { position: fixed; bottom: 24px; left: 50%; transform: translateX(-50%); background: #2f6e60; color: #fff; padding: 12px 20px; border-radius: 12px; font: 700 13px "Hanken Grotesk"; box-shadow: 0 12px 30px -8px rgba(43, 37, 32, 0.4); z-index: 70; }
  @media (max-width: 768px) {
    .page { padding: 16px 16px 92px; }
    .mcp-detail { grid-template-columns: 1fr; }
  }
</style>
