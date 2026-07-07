// Package mcp owns Podiom's canonical MCP catalogue and provider projections.
package mcp

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Source string

const (
	SourcePodiom Source = "podiom"
	SourceClaude Source = "claude"
	SourceCodex  Source = "codex"
)

type Transport string

const (
	TransportHTTP  Transport = "http"
	TransportStdio Transport = "stdio"
)

type EnvStatus struct {
	Name string `json:"name"`
	Set  bool   `json:"set"`
}

type Server struct {
	Name           string      `json:"name" yaml:"name"`
	Transport      Transport   `json:"transport" yaml:"transport"`
	URL            string      `json:"url,omitempty" yaml:"url,omitempty"`
	Command        string      `json:"command,omitempty" yaml:"command,omitempty"`
	Args           []string    `json:"args,omitempty" yaml:"args,omitempty"`
	EnvVars        EnvVars     `json:"env_vars,omitempty" yaml:"env_vars,omitempty"`
	Sources        []Source    `json:"sources,omitempty" yaml:"-"`
	EnvStatus      []EnvStatus `json:"env_status,omitempty" yaml:"-"`
	CodexTablePath string      `json:"-" yaml:"-"`
}

// EnvVar is one environment variable an MCP stdio server needs. Value is
// optional: leave it blank to fall back to whatever the daemon's own OS
// environment already has under Name (the original pass-through model),
// or set it to have Podiom store and inject that value itself.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

// EnvVars is an ordered list of EnvVar. It marshals to/from YAML as a real
// mapping (`env_vars:\n  KEY: "value"`), preserving insertion order rather
// than the alphabetical order a plain Go map would produce, and it accepts
// older catalogue files that stored env_vars as a flow-style list of bare
// names (or, from before validation existed, a stray `KEY="value"` string
// typed into a names-only field) by migrating them into Name/Value pairs.
type EnvVars []EnvVar

func (e EnvVars) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, kv := range e {
		keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv.Name}
		valNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: kv.Value, Style: yaml.DoubleQuotedStyle}
		node.Content = append(node.Content, keyNode, valNode)
	}
	return node, nil
}

func (e *EnvVars) UnmarshalYAML(value *yaml.Node) error {
	*e = nil
	switch value.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(value.Content); i += 2 {
			var name, val string
			if err := value.Content[i].Decode(&name); err != nil {
				return err
			}
			if err := value.Content[i+1].Decode(&val); err != nil {
				return err
			}
			*e = append(*e, EnvVar{Name: name, Value: val})
		}
	case yaml.SequenceNode:
		for _, item := range value.Content {
			var s string
			if err := item.Decode(&s); err != nil {
				return err
			}
			*e = append(*e, parseLegacyEnvVar(s))
		}
	}
	return nil
}

// parseLegacyEnvVar interprets one entry from the pre-value catalogue format:
// a bare name ("FOO") or, from the confusing single-field UI this replaces, a
// name someone typed a value into by hand ("FOO=bar" or `FOO="bar"`).
func parseLegacyEnvVar(s string) EnvVar {
	name, val, ok := strings.Cut(s, "=")
	if !ok {
		return EnvVar{Name: strings.TrimSpace(s)}
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 && val[0] == '"' && val[len(val)-1] == '"' {
		val = val[1 : len(val)-1]
	}
	return EnvVar{Name: strings.TrimSpace(name), Value: val}
}

type Catalogue struct {
	Servers []Server `json:"servers"`
}

type Check struct {
	Server   string `json:"server"`
	Claude   string `json:"claude"`
	Codex    string `json:"codex"`
	Reason   string `json:"reason,omitempty"`
	Assigned bool   `json:"assigned"`
}

type yamlFile struct {
	Servers []Server `yaml:"mcp_servers"`
}

type rawYAMLServer struct {
	Name      string    `yaml:"name"`
	Transport Transport `yaml:"transport"`
	URL       string    `yaml:"url"`
	Command   string    `yaml:"command"`
	Args      []string  `yaml:"args"`
	EnvVars   EnvVars   `yaml:"env_vars"`
	AuthEnv   any       `yaml:"auth_env"`
}

// LoadCatalogue reads Podiom's user catalogue and imports native definitions.
// Native configs are read-only inputs; Podiom never writes them.
func LoadCatalogue(path string) (Catalogue, error) {
	var servers []Server
	user, err := LoadUserFile(path)
	if err != nil {
		return Catalogue{}, err
	}
	servers = append(servers, user...)
	if home, err := os.UserHomeDir(); err == nil {
		if imported, err := ImportClaude(filepath.Join(home, ".claude.json")); err == nil {
			servers = append(servers, imported...)
		}
		if imported, err := ImportCodex(filepath.Join(home, ".codex", "config.toml")); err == nil {
			servers = append(servers, imported...)
		}
	}
	return Catalogue{Servers: dedupe(servers)}, nil
}

func LoadUserFile(path string) ([]Server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read mcp catalogue: %w", err)
	}
	var root struct {
		Servers []rawYAMLServer `yaml:"mcp_servers"`
	}
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("parse mcp catalogue %s: %w", path, err)
	}
	out := make([]Server, 0, len(root.Servers))
	for _, r := range root.Servers {
		s := Server{
			Name:      strings.TrimSpace(r.Name),
			Transport: r.Transport,
			URL:       strings.TrimSpace(r.URL),
			Command:   strings.TrimSpace(r.Command),
			Args:      cleanArgs(r.Args),
			EnvVars:   cleanEnvVars(append(r.EnvVars, envVarsFromLegacy(r.AuthEnv)...)),
			Sources:   []Source{SourcePodiom},
		}
		if err := ValidateServer(s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func SaveUserFile(path string, servers []Server) error {
	var out []Server
	for _, s := range servers {
		if hasSource(s, SourcePodiom) || len(s.Sources) == 0 {
			s.Sources = nil
			s.EnvStatus = nil
			s.CodexTablePath = ""
			s.EnvVars = cleanEnvVars(s.EnvVars)
			if err := ValidateServer(s); err != nil {
				return err
			}
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	raw, err := yaml.Marshal(yamlFile{Servers: out})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, raw, 0o600)
}

func UpsertUserServer(path string, server Server) error {
	server.Sources = []Source{SourcePodiom}
	existing, err := LoadUserFile(path)
	if err != nil {
		return err
	}
	replaced := false
	for i := range existing {
		if existing[i].Name == server.Name {
			existing[i] = server
			replaced = true
			break
		}
	}
	if !replaced {
		existing = append(existing, server)
	}
	return SaveUserFile(path, existing)
}

func RemoveUserServer(path, name string) error {
	existing, err := LoadUserFile(path)
	if err != nil {
		return err
	}
	var kept []Server
	for _, s := range existing {
		if s.Name != name {
			kept = append(kept, s)
		}
	}
	if len(kept) == len(existing) {
		return fmt.Errorf("mcp server %q not found in podiom catalogue", name)
	}
	return SaveUserFile(path, kept)
}

func ValidateServer(s Server) error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("mcp server name is required")
	}
	if !safeName.MatchString(s.Name) {
		return fmt.Errorf("invalid mcp server name %q", s.Name)
	}
	switch s.Transport {
	case TransportHTTP:
		if strings.TrimSpace(s.URL) == "" {
			return fmt.Errorf("mcp server %q: url is required for http transport", s.Name)
		}
	case TransportStdio:
		if strings.TrimSpace(s.Command) == "" {
			return fmt.Errorf("mcp server %q: command is required for stdio transport", s.Name)
		}
	default:
		return fmt.Errorf("mcp server %q: transport must be http or stdio", s.Name)
	}
	for _, kv := range s.EnvVars {
		if !envVarName.MatchString(kv.Name) {
			return fmt.Errorf("mcp server %q: invalid env var name %q (expected a bare NAME like UNIFI_NETWORK_HOST, not a NAME=value pair)", s.Name, kv.Name)
		}
	}
	return nil
}

var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func ImportClaude(path string) ([]Server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var doc struct {
		MCPServers map[string]map[string]any `json:"mcpServers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	var out []Server
	for name, cfg := range doc.MCPServers {
		s := serverFromMap(name, cfg, SourceClaude)
		if s.Name != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func ImportCodex(path string) ([]Server, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	tables := parseTOMLTables(string(raw))
	var out []Server
	for table, cfg := range tables {
		name, ok := codexServerName(table)
		if !ok {
			continue
		}
		if enabled, ok := cfg["enabled"].(bool); ok && !enabled {
			continue
		}
		s := serverFromMap(name, cfg, SourceCodex)
		s.CodexTablePath = table
		if s.Name != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func Assigned(cat Catalogue, names []string) ([]Server, error) {
	byName := map[string]Server{}
	for _, s := range cat.Servers {
		byName[s.Name] = s
	}
	var out []Server
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seen[name] {
			continue
		}
		s, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("assigned mcp server %q is not in the catalogue", name)
		}
		out = append(out, s)
		seen[name] = true
	}
	return out, nil
}

func Checks(cat Catalogue, assigned []string) []Check {
	assignedSet := map[string]bool{}
	for _, name := range assigned {
		assignedSet[name] = true
	}
	out := make([]Check, 0, len(cat.Servers))
	for _, s := range cat.Servers {
		ch := Check{Server: s.Name, Assigned: assignedSet[s.Name]}
		if !ch.Assigned {
			ch.Claude, ch.Codex = "off", "off"
		} else {
			ch.Claude = "native"
			switch s.Transport {
			case TransportStdio:
				ch.Codex = "native"
			case TransportHTTP:
				if HasMCPProxy() {
					ch.Codex = "bridged"
				} else {
					ch.Codex = "unavailable"
					ch.Reason = "mcp-proxy not found in PATH"
				}
			}
		}
		out = append(out, ch)
	}
	return out
}

// resolveEnvPairs turns a server's declared env vars into the pairs that
// should actually be injected into its subprocess: a stored Value wins,
// otherwise it falls back to the daemon's own OS environment (the original
// pass-through model). Entries that resolve to nothing (no stored value and
// unset in the OS environment) are dropped rather than injected as empty.
func resolveEnvPairs(vars EnvVars) EnvVars {
	var out EnvVars
	for _, kv := range vars {
		val := kv.Value
		if val == "" {
			val = os.Getenv(kv.Name)
		}
		if val != "" {
			out = append(out, EnvVar{Name: kv.Name, Value: val})
		}
	}
	return out
}

func ClaudeConfig(servers []Server, permission map[string]any) map[string]any {
	mcpServers := map[string]any{}
	for _, s := range servers {
		mcpServers[s.Name] = claudeServerConfig(s)
	}
	if permission != nil {
		mcpServers["podiom_permission"] = permission
	}
	return map[string]any{"mcpServers": mcpServers}
}

func CodexProfile(assigned []Server, all []Server) (string, []string) {
	assignedSet := map[string]bool{}
	var b strings.Builder
	var unavailable []string
	for _, s := range assigned {
		assignedSet[s.Name] = true
		switch s.Transport {
		case TransportStdio:
			writeCodexServer(&b, tablePath(s), s.Name, s.Command, s.Args, s.EnvVars, true)
		case TransportHTTP:
			if HasMCPProxy() {
				writeCodexServer(&b, tablePath(s), s.Name, "mcp-proxy", []string{s.URL}, s.EnvVars, true)
			} else {
				unavailable = append(unavailable, s.Name)
			}
		}
	}
	for _, s := range all {
		if assignedSet[s.Name] {
			continue
		}
		path := tablePath(s)
		if path == "" {
			continue
		}
		switch s.Transport {
		case TransportStdio:
			writeCodexServer(&b, path, s.Name, s.Command, s.Args, nil, false)
			b.WriteString("enabled = false\n")
		case TransportHTTP:
			if HasMCPProxy() {
				writeCodexServer(&b, path, s.Name, "mcp-proxy", []string{s.URL}, nil, false)
				b.WriteString("enabled = false\n")
			}
		}
	}
	return strings.TrimLeft(b.String(), "\n"), unavailable
}

func CodexConfigOverrides(content string) []string {
	tables := parseTOMLTables(content)
	out := make([]string, 0)
	paths := make([]string, 0, len(tables))
	for path := range tables {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		keys := make([]string, 0, len(tables[path]))
		for key := range tables[path] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			out = append(out, path+"."+key+"="+tomlValue(tables[path][key]))
		}
	}
	return out
}

func ProfileName(agent string) string {
	return "podiom-" + sanitizeName(agent)
}

func ProfileHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])[:16]
}

func WriteCodexProfile(homeDir, agent, content string) (string, string, error) {
	if homeDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", "", err
		}
		homeDir = filepath.Join(home, ".codex")
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		return "", "", err
	}
	name := ProfileName(agent)
	path := filepath.Join(homeDir, name+".config.toml")
	if err := writeFileAtomic(path, []byte(content), 0o600); err != nil {
		return "", "", err
	}
	return name, path, nil
}

func HasMCPProxy() bool {
	_, err := execLookPath("mcp-proxy")
	return err == nil
}

var execLookPath = func(file string) (string, error) {
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		path := filepath.Join(dir, file)
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return path, nil
		}
	}
	return "", os.ErrNotExist
}

func dedupe(in []Server) []Server {
	byName := map[string]Server{}
	var names []string
	for _, s := range in {
		if s.Name == "" {
			continue
		}
		s.EnvVars = cleanEnvVars(s.EnvVars)
		s.EnvStatus = envStatus(s.EnvVars)
		if s.Transport == "" {
			if s.URL != "" {
				s.Transport = TransportHTTP
			} else {
				s.Transport = TransportStdio
			}
		}
		if existing, ok := byName[s.Name]; ok {
			existing.Sources = mergeSources(existing.Sources, s.Sources)
			if existing.Transport == "" {
				existing.Transport = s.Transport
			}
			if existing.URL == "" {
				existing.URL = s.URL
			}
			if existing.Command == "" {
				existing.Command = s.Command
			}
			if len(existing.Args) == 0 {
				existing.Args = append([]string(nil), s.Args...)
			}
			existing.EnvVars = cleanEnvVars(append(existing.EnvVars, s.EnvVars...))
			existing.EnvStatus = envStatus(existing.EnvVars)
			if existing.CodexTablePath == "" {
				existing.CodexTablePath = s.CodexTablePath
			}
			byName[s.Name] = existing
			continue
		}
		if len(s.Sources) == 0 {
			s.Sources = []Source{SourcePodiom}
		}
		byName[s.Name] = s
		names = append(names, s.Name)
	}
	sort.Strings(names)
	out := make([]Server, 0, len(names))
	for _, name := range names {
		out = append(out, byName[name])
	}
	return out
}

func serverFromMap(name string, cfg map[string]any, src Source) Server {
	s := Server{Name: name, Sources: []Source{src}}
	s.Command = stringValue(cfg["command"])
	s.URL = firstNonEmpty(stringValue(cfg["url"]), stringValue(cfg["server_url"]))
	s.Args = stringSlice(cfg["args"])
	if envMap, ok := cfg["env"].(map[string]any); ok {
		for k, v := range envMap {
			s.EnvVars = append(s.EnvVars, EnvVar{Name: k, Value: stringValue(v)})
		}
	}
	for _, name := range stringSlice(cfg["env_vars"]) {
		s.EnvVars = append(s.EnvVars, EnvVar{Name: name})
	}
	s.EnvVars = append(s.EnvVars, envVarsFromLegacy(cfg["auth_env"])...)
	if transport := stringValue(cfg["transport"]); transport != "" {
		s.Transport = Transport(strings.ToLower(transport))
	} else if s.URL != "" {
		s.Transport = TransportHTTP
	} else {
		s.Transport = TransportStdio
	}
	return s
}

func claudeServerConfig(s Server) map[string]any {
	out := map[string]any{}
	if s.Transport == TransportHTTP {
		out["transport"] = "http"
		out["url"] = s.URL
		return out
	}
	out["command"] = s.Command
	if len(s.Args) > 0 {
		out["args"] = s.Args
	}
	if resolved := resolveEnvPairs(s.EnvVars); len(resolved) > 0 {
		env := make(map[string]string, len(resolved))
		for _, kv := range resolved {
			env[kv.Name] = kv.Value
		}
		out["env"] = env
	}
	return out
}

func writeCodexServer(b *strings.Builder, path, name, command string, args []string, env EnvVars, approve bool) {
	if path == "" {
		path = "mcp_servers." + tomlKey(name)
	}
	fmt.Fprintf(b, "\n[%s]\n", path)
	fmt.Fprintf(b, "command = %s\n", tomlString(command))
	if len(args) > 0 {
		fmt.Fprintf(b, "args = %s\n", tomlStringArray(args))
	}
	if resolved := resolveEnvPairs(env); len(resolved) > 0 {
		parts := make([]string, 0, len(resolved))
		for _, kv := range resolved {
			parts = append(parts, fmt.Sprintf("%s = %s", tomlKey(kv.Name), tomlString(kv.Value)))
		}
		fmt.Fprintf(b, "env = { %s }\n", strings.Join(parts, ", "))
	}
	if approve {
		b.WriteString("default_tools_approval_mode = \"approve\"\n")
	}
}

func tablePath(s Server) string {
	if s.CodexTablePath != "" {
		return s.CodexTablePath
	}
	return "mcp_servers." + tomlKey(s.Name)
}

func codexServerName(table string) (string, bool) {
	parts := splitTable(table)
	for i, p := range parts {
		if p == "mcp_servers" && i+1 < len(parts) {
			if i+2 != len(parts) {
				return "", false
			}
			return parts[i+1], true
		}
	}
	return "", false
}

func parseTOMLTables(text string) map[string]map[string]any {
	out := map[string]map[string]any{}
	scanner := bufio.NewScanner(strings.NewReader(text))
	current := ""
	for scanner.Scan() {
		line := strings.TrimSpace(stripTOMLComment(scanner.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			current = strings.Trim(line, "[]")
			out[current] = map[string]any{}
			continue
		}
		if current == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		out[current][strings.TrimSpace(k)] = parseTOMLValue(strings.TrimSpace(v))
	}
	return out
}

func parseTOMLValue(v string) any {
	if v == "true" {
		return true
	}
	if v == "false" {
		return false
	}
	if strings.HasPrefix(v, "[") && strings.HasSuffix(v, "]") {
		body := strings.TrimSpace(v[1 : len(v)-1])
		if body == "" {
			return []string{}
		}
		var out []string
		for _, part := range splitCSV(body) {
			out = append(out, strings.Trim(unquoteTOML(strings.TrimSpace(part)), " "))
		}
		return out
	}
	return unquoteTOML(v)
}

func stripTOMLComment(line string) string {
	inString := false
	escaped := false
	for i, r := range line {
		if escaped {
			escaped = false
			continue
		}
		if r == '\\' && inString {
			escaped = true
			continue
		}
		if r == '"' {
			inString = !inString
			continue
		}
		if r == '#' && !inString {
			return line[:i]
		}
	}
	return line
}

func splitTable(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inQuote {
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if r == '.' && !inQuote {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	parts = append(parts, cur.String())
	return parts
}

func splitCSV(s string) []string {
	var parts []string
	var cur strings.Builder
	inQuote := false
	escaped := false
	for _, r := range s {
		if escaped {
			cur.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' && inQuote {
			cur.WriteRune(r)
			escaped = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			cur.WriteRune(r)
			continue
		}
		if r == ',' && !inQuote {
			parts = append(parts, cur.String())
			cur.Reset()
			continue
		}
		cur.WriteRune(r)
	}
	parts = append(parts, cur.String())
	return parts
}

func unquoteTOML(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		if unquoted, err := strconv.Unquote(s); err == nil {
			return unquoted
		}
		return s[1 : len(s)-1]
	}
	return s
}

func tomlKey(s string) string {
	if regexp.MustCompile(`^[A-Za-z0-9_-]+$`).MatchString(s) {
		return s
	}
	return tomlString(s)
}

func tomlString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

func tomlStringArray(values []string) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, tomlString(v))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func tomlValue(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "true"
		}
		return "false"
	case []string:
		return tomlStringArray(x)
	case string:
		return tomlString(x)
	default:
		return tomlString(fmt.Sprint(x))
	}
}

func envVarsFromLegacy(v any) EnvVars {
	switch x := v.(type) {
	case nil:
		return nil
	case string:
		return EnvVars{{Name: x}}
	case []any:
		var out EnvVars
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, EnvVar{Name: s})
			}
		}
		return out
	case []string:
		var out EnvVars
		for _, s := range x {
			out = append(out, EnvVar{Name: s})
		}
		return out
	default:
		return nil
	}
}

func envStatus(vars EnvVars) []EnvStatus {
	vars = cleanEnvVars(vars)
	out := make([]EnvStatus, 0, len(vars))
	for _, kv := range vars {
		set := kv.Value != ""
		if !set {
			_, set = os.LookupEnv(kv.Name)
		}
		out = append(out, EnvStatus{Name: kv.Name, Set: set})
	}
	return out
}

// cleanArgs trims and drops empty entries but preserves order and duplicates.
// Command-line args are positional (e.g. `--transport streamablehttp <url>`), so
// unlike cleanStrings it must never sort or dedupe them.
func cleanArgs(values []string) []string {
	var out []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// cleanEnvVars trims, drops entries without a name, and dedupes by name while
// preserving first-seen order — env vars are user-ordered like args, so they
// must never sort. A later duplicate's value fills in a still-blank value
// from an earlier occurrence (e.g. merging a name-only import with a
// user-supplied value) rather than being silently dropped.
func cleanEnvVars(values EnvVars) EnvVars {
	seen := map[string]int{}
	var out EnvVars
	for _, kv := range values {
		kv.Name = strings.TrimSpace(kv.Name)
		kv.Value = strings.TrimSpace(kv.Value)
		if kv.Name == "" {
			continue
		}
		if i, ok := seen[kv.Name]; ok {
			if out[i].Value == "" && kv.Value != "" {
				out[i].Value = kv.Value
			}
			continue
		}
		seen[kv.Name] = len(out)
		out = append(out, kv)
	}
	return out
}

func stringSlice(v any) []string {
	switch x := v.(type) {
	case []string:
		return x
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func mergeSources(a, b []Source) []Source {
	seen := map[Source]bool{}
	for _, s := range a {
		seen[s] = true
	}
	for _, s := range b {
		seen[s] = true
	}
	order := []Source{SourcePodiom, SourceClaude, SourceCodex}
	var out []Source
	for _, s := range order {
		if seen[s] {
			out = append(out, s)
		}
	}
	return out
}

func hasSource(s Server, src Source) bool {
	for _, got := range s.Sources {
		if got == src {
			return true
		}
	}
	return false
}

func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "agent"
	}
	return b.String()
}

func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func PrettyYAML(s Server) string {
	raw, _ := yaml.Marshal(yamlFile{Servers: []Server{s}})
	raw = bytes.TrimPrefix(raw, []byte("mcp_servers:\n"))
	return strings.TrimSpace(string(raw))
}
