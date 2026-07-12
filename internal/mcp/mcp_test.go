package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUserFileReadsLegacyAuthEnvAndWritesEnvVars(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	if err := os.WriteFile(path, []byte(`mcp_servers:
  - name: github
    transport: http
    url: https://example.test/mcp
    auth_env: GITHUB_TOKEN
  - name: filesystem
    transport: stdio
    command: npx
    args: ["-y", "@modelcontextprotocol/server-filesystem"]
    env_vars: [PROJECT_ROOT]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadUserFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(servers) != 2 {
		t.Fatalf("servers len = %d", len(servers))
	}
	if got := envNames(servers[0].EnvVars); got != "GITHUB_TOKEN" {
		t.Fatalf("legacy auth_env not mapped to env_vars: %q", got)
	}
	if err := SaveUserFile(path, servers); err != nil {
		t.Fatalf("save: %v", err)
	}
	text := mustRead(t, path)
	if strings.Contains(text, "auth_env") {
		t.Fatalf("save should write canonical env_vars only:\n%s", text)
	}
	if !strings.Contains(text, "env_vars") {
		t.Fatalf("save missing env_vars:\n%s", text)
	}
}

func TestEnvVarsPreserveOrderAndDedupe(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	if err := os.WriteFile(path, []byte(`mcp_servers:
  - name: home-assistant
    transport: stdio
    command: mcp-proxy
    env_vars: [HASS_TOKEN, HASS_TOKEN, HASS_URL, ANTHROPIC_API_KEY]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadUserFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := "HASS_TOKEN,HASS_URL,ANTHROPIC_API_KEY"
	if got := envNames(servers[0].EnvVars); got != want {
		t.Fatalf("env_vars order/dedupe = %q, want %q", got, want)
	}
	if err := SaveUserFile(path, servers); err != nil {
		t.Fatalf("save: %v", err)
	}
	reloaded, err := LoadUserFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := envNames(reloaded[0].EnvVars); got != want {
		t.Fatalf("env_vars order not preserved after save/reload = %q, want %q", got, want)
	}
}

func envNames(vars EnvVars) string {
	names := make([]string, len(vars))
	for i, kv := range vars {
		names[i] = kv.Name
	}
	return strings.Join(names, ",")
}

func TestEnvVarsStoreValuesAndRoundTripAsYAMLMapping(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	server := Server{
		Name:      "unifi-network",
		Transport: TransportStdio,
		Command:   "/usr/local/bin/uvx",
		Args:      []string{"unifi-network-mcp@latest"},
		EnvVars: EnvVars{
			{Name: "UNIFI_NETWORK_HOST", Value: "192.168.1.7"},
			{Name: "UNIFI_NETWORK_PORT", Value: "8443"},
			{Name: "UNIFI_NETWORK_USERNAME", Value: "mar-schmidt"},
			{Name: "UNIFI_NETWORK_PASSWORD", Value: "D69H3rmgY7"},
		},
	}
	if err := UpsertUserServer(path, server); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	text := mustRead(t, path)
	if strings.Contains(text, "[") && strings.Contains(text, "=") {
		t.Fatalf("expected a real YAML mapping, not a flow-style KEY=value list:\n%s", text)
	}
	if !strings.Contains(text, `UNIFI_NETWORK_HOST: "192.168.1.7"`) {
		t.Fatalf("expected env_vars written as a YAML mapping with real values:\n%s", text)
	}

	servers, err := LoadUserFile(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers len = %d", len(servers))
	}
	if got := envNames(servers[0].EnvVars); got != "UNIFI_NETWORK_HOST,UNIFI_NETWORK_PORT,UNIFI_NETWORK_USERNAME,UNIFI_NETWORK_PASSWORD" {
		t.Fatalf("order not preserved: %q", got)
	}
	for _, want := range server.EnvVars {
		var got string
		for _, kv := range servers[0].EnvVars {
			if kv.Name == want.Name {
				got = kv.Value
			}
		}
		if got != want.Value {
			t.Fatalf("env var %s value = %q, want %q", want.Name, got, want.Value)
		}
	}
}

func TestEnvVarsMigrateLegacyKeyEqualsValueString(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mcp.yaml")
	if err := os.WriteFile(path, []byte(`mcp_servers:
  - name: unifi-network
    transport: stdio
    command: /usr/local/bin/uvx
    args: ["unifi-network-mcp@latest"]
    env_vars: [UNIFI_NETWORK_HOST="192.168.1.7", UNIFI_NETWORK_PORT="8443", BARE_NAME]
`), 0o600); err != nil {
		t.Fatal(err)
	}
	servers, err := LoadUserFile(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers len = %d", len(servers))
	}
	got := servers[0].EnvVars
	want := EnvVars{
		{Name: "UNIFI_NETWORK_HOST", Value: "192.168.1.7"},
		{Name: "UNIFI_NETWORK_PORT", Value: "8443"},
		{Name: "BARE_NAME", Value: ""},
	}
	if len(got) != len(want) {
		t.Fatalf("migrated env_vars = %+v, want %+v", got, want)
	}
	for i, kv := range want {
		if got[i] != kv {
			t.Fatalf("migrated env_vars[%d] = %+v, want %+v", i, got[i], kv)
		}
	}
}

func TestValidateServerRejectsMalformedEnvVarName(t *testing.T) {
	s := Server{
		Name:      "bad",
		Transport: TransportStdio,
		Command:   "echo",
		EnvVars:   EnvVars{{Name: `FOO="bar"`, Value: ""}},
	}
	if err := ValidateServer(s); err == nil {
		t.Fatal("expected validation error for malformed env var name")
	}
}

func TestClaudeAndCodexConfigsInjectResolvedEnvValues(t *testing.T) {
	t.Setenv("FROM_OS_ENV", "os-value")
	server := Server{
		Name:      "unifi-network",
		Transport: TransportStdio,
		Command:   "/usr/local/bin/uvx",
		Args:      []string{"unifi-network-mcp@latest"},
		EnvVars: EnvVars{
			{Name: "UNIFI_NETWORK_PASSWORD", Value: "D69H3rmgY7"},
			{Name: "FROM_OS_ENV"},
		},
	}
	claudeCfg := ClaudeConfig([]Server{server}, nil)
	mcpServers, ok := claudeCfg["mcpServers"].(map[string]any)
	if !ok {
		t.Fatalf("claude config missing mcpServers: %+v", claudeCfg)
	}
	entry, ok := mcpServers["unifi-network"].(map[string]any)
	if !ok {
		t.Fatalf("claude config missing server entry: %+v", mcpServers)
	}
	env, ok := entry["env"].(map[string]string)
	if !ok {
		t.Fatalf("claude config server entry missing env map: %+v", entry)
	}
	if env["UNIFI_NETWORK_PASSWORD"] != "D69H3rmgY7" {
		t.Fatalf("claude config env stored value = %q", env["UNIFI_NETWORK_PASSWORD"])
	}
	if env["FROM_OS_ENV"] != "os-value" {
		t.Fatalf("claude config env os-fallback value = %q", env["FROM_OS_ENV"])
	}

	profile, _ := CodexProfile([]Server{server}, nil)
	if !strings.Contains(profile, `UNIFI_NETWORK_PASSWORD = "D69H3rmgY7"`) {
		t.Fatalf("codex profile missing stored env value:\n%s", profile)
	}
	if !strings.Contains(profile, `FROM_OS_ENV = "os-value"`) {
		t.Fatalf("codex profile missing os-fallback env value:\n%s", profile)
	}
}

func TestCodexConfigOverridesPreserveInlineEnvTable(t *testing.T) {
	server := Server{
		Name:      "unifi-network",
		Transport: TransportStdio,
		Command:   "/usr/local/bin/uvx",
		Args:      []string{"unifi-network-mcp@latest"},
		EnvVars: EnvVars{
			{Name: "UNIFI_NETWORK_HOST", Value: "192.168.1.7"},
			{Name: "UNIFI_NETWORK_PORT", Value: "8443"},
			{Name: "UNIFI_NETWORK_USERNAME", Value: "mar-schmidt"},
			{Name: "UNIFI_NETWORK_PASSWORD", Value: `D69H3rmg,Y7"`},
		},
	}

	profile, _ := CodexProfile([]Server{server}, nil)
	overrides := strings.Join(CodexConfigOverrides(profile), "\n")
	if strings.Contains(overrides, `mcp_servers.unifi-network.env="{`) {
		t.Fatalf("env override should be an inline table, not a string:\n%s", overrides)
	}
	for _, want := range []string{
		`mcp_servers.unifi-network.env={UNIFI_NETWORK_HOST="192.168.1.7", UNIFI_NETWORK_PASSWORD="D69H3rmg,Y7\"", UNIFI_NETWORK_PORT="8443", UNIFI_NETWORK_USERNAME="mar-schmidt"}`,
		`mcp_servers.unifi-network.args=["unifi-network-mcp@latest"]`,
		`mcp_servers.unifi-network.default_tools_approval_mode="approve"`,
	} {
		if !strings.Contains(overrides, want) {
			t.Fatalf("overrides missing %q:\n%s", want, overrides)
		}
	}
}

func TestImportNativeConfigsAndEnvStatus(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "present")
	dir := t.TempDir()
	claudePath := filepath.Join(dir, "claude.json")
	if err := os.WriteFile(claudePath, []byte(`{"mcpServers":{"github":{"transport":"http","url":"https://example.test/mcp","env":{"GITHUB_TOKEN":"secret"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	claude, err := ImportClaude(claudePath)
	if err != nil {
		t.Fatalf("import claude: %v", err)
	}
	if len(claude) != 1 || claude[0].Name != "github" || claude[0].Transport != TransportHTTP {
		t.Fatalf("bad claude import: %+v", claude)
	}

	codexPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(codexPath, []byte(`
[mcp_servers.postgres]
command = "npx"
args = ["-y", "@modelcontextprotocol/server-postgres"]

[plugins."computer-use@openai-bundled".mcp_servers.computer-use]
command = "computer-use"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	codex, err := ImportCodex(codexPath)
	if err != nil {
		t.Fatalf("import codex: %v", err)
	}
	if len(codex) != 2 {
		t.Fatalf("codex import len = %d: %+v", len(codex), codex)
	}
	merged := dedupe(append(claude, codex...))
	var github Server
	for _, s := range merged {
		if s.Name == "github" {
			github = s
		}
	}
	if len(github.EnvStatus) != 1 || !github.EnvStatus[0].Set {
		t.Fatalf("env status not populated: %+v", github.EnvStatus)
	}
}

func TestImportCodexIgnoresNestedMCPChildTables(t *testing.T) {
	dir := t.TempDir()
	codexPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(codexPath, []byte(`
[mcp_servers.node_repl]
command = "/Applications/Codex.app/Contents/Resources/cua_node/bin/node_repl"
args = []
startup_timeout_sec = 120

[mcp_servers.node_repl.env]
NODE_REPL_NATIVE_PIPE_CONNECT_TIMEOUT_MS = "1000"
CODEX_CLI_PATH = "/Applications/Codex.app/Contents/Resources/codex"

[mcp_servers.node_repl.headers]
Authorization = "Bearer secret"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := ImportCodex(codexPath)
	if err != nil {
		t.Fatalf("import codex: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("codex import len = %d: %+v", len(servers), servers)
	}
	if servers[0].Name != "node_repl" {
		t.Fatalf("imported wrong server: %+v", servers[0])
	}
	if servers[0].CodexTablePath != "mcp_servers.node_repl" {
		t.Fatalf("codex table path = %q", servers[0].CodexTablePath)
	}
}

func TestCodexProfileDoesNotDisableNestedChildTables(t *testing.T) {
	dir := t.TempDir()
	codexPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(codexPath, []byte(`
[mcp_servers.node_repl]
command = "/Applications/Codex.app/Contents/Resources/cua_node/bin/node_repl"

[mcp_servers.node_repl.env]
CODEX_CLI_PATH = "/Applications/Codex.app/Contents/Resources/codex"
`), 0o600); err != nil {
		t.Fatal(err)
	}

	servers, err := ImportCodex(codexPath)
	if err != nil {
		t.Fatalf("import codex: %v", err)
	}
	profile, unavailable := CodexProfile(nil, servers)
	if len(unavailable) != 0 {
		t.Fatalf("unexpected unavailable: %+v", unavailable)
	}
	if strings.Contains(profile, "mcp_servers.node_repl.env") {
		t.Fatalf("profile should not include nested env table:\n%s", profile)
	}
	if !strings.Contains(profile, "[mcp_servers.node_repl]") || !strings.Contains(profile, "enabled = false") {
		t.Fatalf("profile should disable valid top-level native server:\n%s", profile)
	}

	overrides := strings.Join(CodexConfigOverrides(profile), "\n")
	if strings.Contains(overrides, "mcp_servers.node_repl.env.enabled=false") {
		t.Fatalf("overrides should not disable nested env table:\n%s", overrides)
	}
	if !strings.Contains(overrides, "mcp_servers.node_repl.enabled=false") {
		t.Fatalf("overrides should disable top-level native server:\n%s", overrides)
	}
}

func TestCodexProfileDisablesUnassignedAndBridgesHTTP(t *testing.T) {
	old := execLookPath
	execLookPath = func(file string) (string, error) { return "/usr/local/bin/" + file, nil }
	defer func() { execLookPath = old }()

	assigned := []Server{{
		Name:      "github",
		Transport: TransportHTTP,
		URL:       "https://example.test/mcp",
	}}
	all := append(assigned, Server{
		Name:           "computer-use",
		Transport:      TransportStdio,
		Command:        "computer-use",
		CodexTablePath: `plugins."computer-use@openai-bundled".mcp_servers.computer-use`,
	})
	profile, unavailable := CodexProfile(assigned, all)
	if len(unavailable) != 0 {
		t.Fatalf("unexpected unavailable: %+v", unavailable)
	}
	for _, want := range []string{
		"[mcp_servers.github]",
		`command = "mcp-proxy"`,
		`args = ["https://example.test/mcp"]`,
		`default_tools_approval_mode = "approve"`,
		`[plugins."computer-use@openai-bundled".mcp_servers.computer-use]`,
		"enabled = false",
	} {
		if !strings.Contains(profile, want) {
			t.Fatalf("profile missing %q:\n%s", want, profile)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
