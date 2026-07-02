<script lang="ts">
  import { onMount } from "svelte";
  import { getMCP, listSkills, saveMCPServer, setMCPAssignment } from "../lib/api";
  import type { MCPAgent, MCPSnapshot, MCPServer, MCPSource, Skill, SkillSource } from "../lib/types";

  let skills = $state<Skill[]>([]);
  let mcp = $state<MCPSnapshot>({ servers: [], agents: [], assignments: {} });
  let loadError = $state<string | null>(null);

  let tab = $state<"skills" | "mcp">("mcp");
  let query = $state("");
  let sourceFilter = $state<"all" | SkillSource>("all");
  let open = $state<Record<string, boolean>>({});
  let mcpOpen = $state<Record<string, boolean>>({});

  let addOpen = $state(false);
  let addName = $state("");
  let addTransport = $state<"http" | "stdio">("stdio");
  let addEndpoint = $state("");
  let addEnvVars = $state("");
  let savingServer = $state(false);

  onMount(async () => {
    try {
      const [sk, snapshot] = await Promise.all([listSkills(), getMCP()]);
      skills = sk;
      mcp = snapshot;
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    }
  });

  const SKILL_SRC: Record<SkillSource, { label: string; fg: string; bg: string; bd: string; glyph: string; path: string }> = {
    agents: { label: "shared", fg: "#2F6E60", bg: "#E7F0EC", bd: "#C7DBD2", glyph: "circle", path: "~/.agents/skills/" },
    claude: { label: "claude", fg: "#B0572F", bg: "#F8EBE2", bd: "#ECD3C2", glyph: "square", path: "~/.claude/skills/" },
    codex: { label: "codex", fg: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2", glyph: "diamond", path: "~/.codex/skills/" },
  };
  const MCP_SRC: Record<MCPSource, { label: string; fg: string; bg: string; bd: string; glyph: string }> = {
    podiom: { label: "podiom", fg: "#2F6E60", bg: "#E7F0EC", bd: "#C7DBD2", glyph: "circle" },
    claude: { label: "claude", fg: "#B0572F", bg: "#F8EBE2", bd: "#ECD3C2", glyph: "square" },
    codex: { label: "codex", fg: "#4B5560", bg: "#EAEEF1", bd: "#D6DCE2", glyph: "diamond" },
  };
  const ORDER: SkillSource[] = ["agents", "claude", "codex"];

  function dot(color: string, glyph = "circle"): string {
    const shape = glyph === "circle" ? "border-radius:99px" : glyph === "square" ? "border-radius:2px" : "border-radius:1px;transform:rotate(45deg)";
    return `width:8px;height:8px;background:${color};flex:none;${shape}`;
  }
  function chip(fg: string, bg: string, bd: string): string {
    return `display:inline-flex;align-items:center;gap:6px;padding:4px 9px;border-radius:8px;font:600 11px 'JetBrains Mono',monospace;color:${fg};background:${bg};border:1px solid ${bd};white-space:nowrap`;
  }
  function skillChip(src: SkillSource): string {
    const s = SKILL_SRC[src];
    return chip(s.fg, s.bg, s.bd);
  }
  function mcpChip(src: MCPSource): string {
    const s = MCP_SRC[src];
    return chip(s.fg, s.bg, s.bd);
  }

  const counts = $derived.by(() => {
    const c = { all: skills.length, agents: 0, claude: 0, codex: 0 };
    for (const sk of skills) for (const s of sk.sources) c[s]++;
    return c;
  });

  const filteredSkills = $derived.by(() => {
    const q = query.trim().toLowerCase();
    return skills.filter((sk) => {
      if (sourceFilter !== "all" && !sk.sources.includes(sourceFilter)) return false;
      if (q && !(sk.name.toLowerCase().includes(q) || sk.description.toLowerCase().includes(q))) return false;
      return true;
    });
  });

  const flowSources = $derived(
    ORDER.map((key) => ({
      key,
      label: SKILL_SRC[key].label,
      path: SKILL_SRC[key].path,
      count: skills.filter((sk) => sk.sources.includes(key)).length,
    })),
  );

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
        const parts = splitCommand(addEndpoint.trim());
        server.command = parts[0] ?? "";
        server.args = parts.slice(1);
      }
      mcp = await saveMCPServer(server);
      addOpen = false;
      addName = "";
      addEndpoint = "";
      addEnvVars = "";
      addTransport = "stdio";
    } catch (e) {
      loadError = e instanceof Error ? e.message : String(e);
    } finally {
      savingServer = false;
    }
  }

  function splitCommand(value: string): string[] {
    return value.match(/"[^"]+"|'[^']+'|\S+/g)?.map((s) => s.replace(/^['"]|['"]$/g, "")) ?? [];
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
      <button class:active={tab === "skills"} onclick={() => (tab = "skills")}>Skills <span>{skills.length}</span></button>
      <button class:active={tab === "mcp"} onclick={() => (tab = "mcp")}>MCP servers <span>{mcp.servers.length}</span></button>
    </div>

    {#if loadError}
      <div class="error">Could not load: {loadError}</div>
    {/if}

    {#if tab === "skills"}
      <section class="note">A read-only catalogue of reusable SKILL.md files found on this machine.</section>

      <section class="flow-card">
        <div class="section-title"><span>How skills reach your agents</span><i></i><em>Wherever a skill lives, it joins one pool.</em></div>
        <div class="flow">
          <div class="sources">
            {#each flowSources as g (g.key)}
              <div class="source-card">
                <div><span style={skillChip(g.key)}><span style={dot(SKILL_SRC[g.key].fg, SKILL_SRC[g.key].glyph)}></span>{g.label}</span><b>{g.count}</b></div>
                <code>{g.path}</code>
              </div>
            {/each}
          </div>
          <div class="pool">
            <div class="pool-head"><b>Podiom pool</b><span>{skills.length} skills</span></div>
            <div class="pool-list">
              {#each skills as s (s.name)}
                <span>{s.name}</span>
              {/each}
            </div>
          </div>
        </div>
      </section>

      <div class="toolbar">
        <input bind:value={query} placeholder="Search skills by name or description..." />
        <div class="pills">
          {#each ["all", "agents", "claude", "codex"] as p}
            <button class:active={sourceFilter === p} onclick={() => (sourceFilter = p as "all" | SkillSource)}>
              {p === "all" ? "All" : SKILL_SRC[p as SkillSource].label}
              <span>{counts[p as keyof typeof counts]}</span>
            </button>
          {/each}
        </div>
      </div>

      <div class="rows">
        {#each filteredSkills as s (s.name)}
          <article class="row">
            <button class="row-head" onclick={() => (open = { ...open, [s.name]: !open[s.name] })}>
              <span class:rot={open[s.name]} class="chev">›</span>
              <div><b>{s.name}</b><p>{s.description}</p></div>
              <div class="badges">
                {#each s.sources as src (src)}
                  <span style={skillChip(src)}><span style={dot(SKILL_SRC[src].fg, SKILL_SRC[src].glyph)}></span>{SKILL_SRC[src].label}</span>
                {/each}
              </div>
            </button>
            {#if open[s.name]}
              <div class="expanded">
                {#each s.locations as loc (loc.source)}
                  <div class="loc"><span style={skillChip(loc.source)}>{SKILL_SRC[loc.source].label}</span><code>{loc.path}</code></div>
                {/each}
                {#each s.contents as c, i (c.source || i)}
                  <pre>{c.body}</pre>
                {/each}
              </div>
            {/if}
          </article>
        {/each}
      </div>
    {:else}
      <section class="note">MCP servers are assigned per agent. A cell grants or revokes a server for that agent.</section>

      <section class="mcp-card">
        <div class="mcp-head">
          <div><b>Assignment matrix</b><p>Each assigned set is projected into Claude and Codex at launch.</p></div>
          <button class="primary" onclick={() => (addOpen = !addOpen)}>+ Add server</button>
        </div>

        {#if addOpen}
          <div class="add-box">
            <input bind:value={addName} placeholder="server name" />
            <div class="transport">
              <button class:active={addTransport === "stdio"} onclick={() => (addTransport = "stdio")}>stdio</button>
              <button class:active={addTransport === "http"} onclick={() => (addTransport = "http")}>http</button>
            </div>
            <input class="wide" bind:value={addEndpoint} placeholder={addTransport === "http" ? "https://..." : "npx -y @scope/mcp-server"} />
            <input bind:value={addEnvVars} placeholder="ENV_NAMES comma separated" />
            <button class="primary" disabled={savingServer || !addName.trim() || !addEndpoint.trim()} onclick={addServer}>{savingServer ? "Saving..." : "Save"}</button>
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
                    <b>Projection</b>
                    <p>Claude: strict --mcp-config. Codex: generated profile with unassigned known servers disabled{server.transport === "http" ? ", HTTP bridged through mcp-proxy when present" : ""}.</p>
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

<style>
  .page { flex: 1; overflow-y: auto; padding: 24px 28px 0; min-height: 0; background: #f4ece2; color: #2b2520; }
  .inner { max-width: 1080px; margin: 0 auto; }
  .head h1 { margin: 0; font: 800 24px "Hanken Grotesk"; letter-spacing: -0.02em; }
  .head p, .note { margin: 4px 0 0; font: 400 13px/1.55 "Hanken Grotesk"; color: #8a7f73; max-width: 650px; }
  .tabs { display: inline-flex; gap: 4px; padding: 4px; margin-top: 18px; border-radius: 13px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .tabs button, .pills button, .transport button { border: 0; background: transparent; color: #8a7f73; cursor: pointer; border-radius: 9px; padding: 7px 12px; font: 700 12.5px "Hanken Grotesk"; }
  .tabs button.active, .pills button.active, .transport button.active { background: #fffdfb; color: #2b2520; box-shadow: 0 1px 3px rgba(43,37,32,.12); }
  .tabs span, .pills span { margin-left: 6px; font: 600 11px "JetBrains Mono", monospace; color: #a89c8e; }
  .error { margin-top: 16px; padding: 12px 14px; border-radius: 12px; background: #f8ebe2; border: 1px solid #ecd3c2; color: #b0572f; font: 600 13px "Hanken Grotesk"; }
  .flow-card, .mcp-card, .row { margin-top: 18px; background: #fffdfb; border: 1px solid #ede4d9; border-radius: 16px; box-shadow: 0 1px 2px rgba(43,37,32,.04), 0 18px 44px -32px rgba(43,37,32,.22); overflow: hidden; }
  .flow-card { padding: 18px 20px; }
  .section-title { display: flex; align-items: center; gap: 9px; margin-bottom: 16px; }
  .section-title span { font: 600 10px "JetBrains Mono", monospace; letter-spacing: .13em; color: #a89c8e; text-transform: uppercase; }
  .section-title i { flex: 1; height: 1px; background: #f1eae0; }
  .section-title em { font: 400 12px "Hanken Grotesk"; color: #8a7f73; font-style: normal; }
  .flow { display: grid; grid-template-columns: 236px 1fr; gap: 14px; }
  .sources { display: flex; flex-direction: column; gap: 12px; }
  .source-card { background: #fbf7f1; border: 1px solid #ede4d9; border-radius: 13px; padding: 13px 14px; }
  .source-card div { display: flex; align-items: center; gap: 8px; }
  .source-card b { margin-left: auto; font: 600 11px "JetBrains Mono", monospace; color: #b7ac9e; }
  code { font: 500 12px "JetBrains Mono", monospace; color: #7a6f62; }
  .pool { background: #fffdfb; border: 1px solid #dcd0c1; border-radius: 14px; padding: 15px 17px; }
  .pool-head { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
  .pool-head b { font: 800 15px "Hanken Grotesk"; }
  .pool-head span, .transport-chip, .env-chip { padding: 4px 9px; border-radius: 8px; background: #eaf2ee; border: 1px solid #cfe2da; color: #2f6e60; font: 600 11px "JetBrains Mono", monospace; }
  .pool-list { display: flex; flex-wrap: wrap; gap: 7px; }
  .pool-list span { padding: 5px 10px; border-radius: 8px; background: #fbf7f1; border: 1px solid #efe6db; font: 600 11.5px "JetBrains Mono", monospace; }
  .toolbar { display: flex; gap: 12px; flex-wrap: wrap; margin-top: 22px; }
  input { padding: 10px 12px; border: 1px solid #eae0d4; border-radius: 11px; background: #fffdfb; font: 500 13px "Hanken Grotesk"; color: #2b2520; outline: none; }
  .toolbar input { flex: 1; min-width: 240px; max-width: 420px; }
  .pills { display: flex; gap: 4px; padding: 4px; border-radius: 12px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .rows { display: flex; flex-direction: column; gap: 11px; margin-top: 18px; }
  .row-head, .server-cell { width: 100%; border: 0; background: transparent; text-align: left; cursor: pointer; display: flex; gap: 12px; align-items: flex-start; }
  .row-head { padding: 16px 20px; }
  .row-head b, .server-cell b { font: 700 15px "JetBrains Mono", monospace; color: #241f1a; }
  .row-head p { margin: 5px 0 0; font: 400 13px/1.5 "Hanken Grotesk"; color: #6f6459; }
  .chev { display: inline-flex; width: 18px; height: 18px; align-items: center; justify-content: center; color: #b7ac9e; font-size: 22px; transition: transform .16s ease; }
  .chev.rot { transform: rotate(90deg); }
  .badges { margin-left: auto; display: flex; gap: 6px; flex-wrap: wrap; justify-content: flex-end; }
  .expanded, .mcp-detail { border-top: 1px solid #f1eae0; padding: 16px 22px 20px; }
  .loc { display: flex; gap: 10px; align-items: center; margin-bottom: 7px; }
  pre { margin: 12px 0 0; background: #fbf7f1; border: 1px solid #efe6db; border-radius: 12px; padding: 14px 16px; font: 500 12px/1.65 "JetBrains Mono", monospace; color: #4a4138; white-space: pre-wrap; word-break: break-word; }
  .mcp-head { display: flex; gap: 14px; align-items: center; justify-content: space-between; padding: 16px 20px; border-bottom: 1px solid #f1eae0; }
  .mcp-head b { font: 800 15px "Hanken Grotesk"; }
  .mcp-head p { margin: 2px 0 0; font: 400 11.5px "Hanken Grotesk"; color: #8a7f73; }
  .primary { border: 0; border-radius: 11px; background: #3f8f7e; color: #fff; padding: 9px 14px; font: 800 13px "Hanken Grotesk"; cursor: pointer; }
  .primary:disabled { opacity: .55; cursor: default; }
  .add-box { display: flex; gap: 10px; flex-wrap: wrap; padding: 14px 20px; background: #fbf7f1; border-bottom: 1px solid #f1eae0; }
  .add-box .wide { flex: 1; min-width: 260px; }
  .transport { display: flex; gap: 4px; padding: 4px; border-radius: 11px; background: #efe7dc; border: 1px solid #e6dbcc; }
  .matrix { overflow-x: auto; padding: 6px 20px 14px; }
  .matrix-row { display: flex; align-items: center; gap: 6px; min-width: max-content; border-bottom: 1px solid #f5eee4; }
  .matrix-top { padding: 14px 0 12px; color: #b7ac9e; font: 600 10px "JetBrains Mono", monospace; text-transform: uppercase; }
  .server-col, .server-cell { width: 300px; flex: none; }
  .server-cell { padding: 12px 0; }
  .server-cell p { margin: 7px 0 0; display: flex; gap: 7px; flex-wrap: wrap; }
  .agent-col { width: 92px; flex: none; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 4px; }
  .agent-col b { font: 700 12px "Hanken Grotesk"; color: #3a332c; text-transform: none; }
  .agent-col span { font: 600 10px "JetBrains Mono", monospace; color: #8a7f73; text-transform: none; }
  .transport-chip { background: #efe7dc; border-color: #e6dbcc; color: #8a7560; }
  .env-chip { background: #fbf7f1; border-color: #efe6db; color: #7a6f62; }
  .mcp-detail { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; padding-left: 22px; }
  .mcp-detail b { font: 700 13px "Hanken Grotesk"; }
  .mcp-detail p { margin: 6px 0 0; font: 400 12.5px/1.55 "Hanken Grotesk"; color: #7a6f62; }
  @media (max-width: 768px) {
    .page { padding: 16px 16px 92px; }
    .flow { grid-template-columns: 1fr; }
    .toolbar { flex-direction: column; }
    .toolbar input { max-width: none; }
    .mcp-detail { grid-template-columns: 1fr; }
  }
</style>
