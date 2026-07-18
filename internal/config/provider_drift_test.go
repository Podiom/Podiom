package config

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestProviderKnowledgeStaysInRegistry guards the provider architecture:
// provider identity ("claude"/"codex" and the ProviderClaude/ProviderCodex
// constants) must not be branched on outside the sanctioned locations below.
// Everything else derives behavior from the ProviderInfo registry (Go), the
// per-layer tables it points at, or web/src/lib/providers.ts (frontend).
//
// If this test fails on your change, do not add the file to the allowlist —
// route the new behavior through the registry or a per-layer table instead.
// See the doc comment on providerInfos and docs/integrations/README.md.
// Extending the allowlist is correct only for a genuinely new sanctioned
// location (e.g. a new per-layer table for a new subsystem).
func TestProviderKnowledgeStaysInRegistry(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	// Repo-relative files/prefixes where provider identity may appear in code.
	allowed := []string{
		"internal/adapter/",               // the provider implementations themselves
		"internal/config/provider.go",     // the registry
		"internal/config/config.go",       // the Provider constants
		"internal/usage/providers.go",     // per-layer table: usage fetchers
		"internal/usage/claude.go",        // per-provider usage endpoint
		"internal/usage/codex.go",         // per-provider usage endpoint
		"internal/providercheck/",         // per-layer table: CLI auth probes
		"internal/claudeauth/",            // Claude credential-path sidecar
		"internal/mcp/mcp.go",             // Source constants, API-frozen Check fields, native imports
		"internal/skills/skills.go",       // Source constants + nativeRoots table
		"cmd/podiomd/main.go",             // adapter composition root
		"cmd/podiom/main.go",              // CLI flag defaults/help prose
		"internal/store/migrate.go",       // shipped migrations are frozen history (v25 dropped the CHECKs)
		"web/src/lib/providers.ts",        // the frontend registry
		"web/src/lib/logos/",              // per-provider logo components
	}
	skipDirs := map[string]bool{
		".git": true, "node_modules": true, "dist": true, "bin": true,
		"docs": true, "ha": true, "scripts": true, "testdata": true,
	}

	// Exact quoted provider tokens ("claude", 'codex', `claude`). Substrings in
	// prose ("claude or codex") and dot-paths (".claude") do not match.
	literal := regexp.MustCompile("[\"'`](claude|codex)[\"'`]")
	constant := regexp.MustCompile(`\bProvider(Claude|Codex)\b`)

	err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)

		isGo := strings.HasSuffix(rel, ".go") && !strings.HasSuffix(rel, "_test.go")
		isWeb := strings.HasPrefix(rel, "web/src/") &&
			(strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".svelte"))
		if !isGo && !isWeb {
			return nil
		}
		for _, a := range allowed {
			if rel == a || strings.HasPrefix(rel, a) {
				return nil
			}
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)
		lineNo := 0
		for scanner.Scan() {
			lineNo++
			line := scanner.Text()
			// Comments may mention providers as prose; strip them before
			// matching (crude for // inside string literals, fine here).
			if i := strings.Index(line, "//"); i >= 0 {
				line = line[:i]
			}
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "*") || strings.HasPrefix(trimmed, "<!--") {
				continue
			}
			if literal.MatchString(line) || (isGo && constant.MatchString(line)) {
				t.Errorf("%s:%d: provider identity outside the sanctioned locations: %s\n"+
					"\troute this through config.ProviderInfo / web/src/lib/providers.ts instead "+
					"(see providerInfos doc comment)", rel, lineNo, trimmed)
			}
		}
		return scanner.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}
