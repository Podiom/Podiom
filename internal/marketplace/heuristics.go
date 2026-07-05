package marketplace

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Static heuristic scan (SEC-6/7). These are lightweight, best-effort signals
// shown to the user as WARNINGS — Podiom informs, the user decides. They are not
// a scanner verdict and never block an install on their own. All checks are
// static: no file from a skill is ever executed (SEC-4).

type scanRule struct {
	rule     string
	severity string
	message  string
	re       *regexp.Regexp
}

var (
	// Script/network heuristics run over executable-ish text files.
	scriptRules = []scanRule{
		{"pipe-to-shell", "warn", "Downloads and pipes a remote script straight into a shell (curl|sh / wget|sh).",
			regexp.MustCompile(`(?i)\b(curl|wget)\b[^\n|]*\|\s*(sudo\s+)?(sh|bash|zsh)\b`)},
		{"network-call", "info", "Makes outbound network calls.",
			regexp.MustCompile(`(?i)\b(curl|wget|urllib|requests\.(get|post)|http\.(get|post)|fetch\(|net/http|axios)\b`)},
		{"credential-path", "warn", "References credential or key locations (~/.ssh, ~/.aws, keychain, .env).",
			regexp.MustCompile(`(?i)(\.ssh/|\.aws/|/\.aws\b|security\s+find-generic-password|keychain|id_rsa|\.env\b|credentials)`)},
		{"base64-blob", "warn", "Contains a large base64 blob (possible obfuscated payload).",
			regexp.MustCompile(`[A-Za-z0-9+/]{240,}={0,2}`)},
		{"eval-exec", "warn", "Uses dynamic code execution (eval / exec / os.system).",
			regexp.MustCompile(`(?i)\b(eval|exec|os\.system|subprocess\.(call|run|Popen)|child_process)\b`)},
	}
	// Prompt-injection heuristics run over SKILL.md prose (untrusted prompt content).
	promptRules = []scanRule{
		{"prompt-exfil", "warn", "SKILL.md text appears to instruct the agent to send or exfiltrate data.",
			regexp.MustCompile(`(?i)(exfiltrat|send (the )?(your |their )?(api |secret |private )?(keys?|tokens?|credentials?|password)|upload .*(secret|token|credential)|POST .*(token|secret))`)},
		{"prompt-remote-instructions", "warn", "SKILL.md text appears to direct the agent to fetch and follow remote instructions.",
			regexp.MustCompile(`(?i)(fetch|download|curl|retrieve) .*(instruction|prompt|command).*(from|at) https?://`)},
		{"prompt-ignore", "info", "SKILL.md contains override phrasing ('ignore previous instructions').",
			regexp.MustCompile(`(?i)ignore (all |the )?(previous|prior|above) (instructions|rules|prompts)`)},
	}
)

// scannableText reports whether a file is worth text-scanning by extension /
// location, and whether it is script-like (network/credential rules apply).
func scannableText(rel string) (scan, script bool) {
	lower := strings.ToLower(rel)
	base := filepath.Base(lower)
	if strings.HasPrefix(lower, "scripts/") || strings.Contains(lower, "/scripts/") {
		script = true
	}
	switch filepath.Ext(base) {
	case ".sh", ".bash", ".zsh", ".py", ".js", ".ts", ".mjs", ".cjs", ".rb", ".pl", ".ps1":
		return true, true
	case ".md", ".txt", ".yaml", ".yml", ".json", ".toml", ".cfg", ".ini":
		return true, script
	case "":
		// Extensionless files in scripts/ are commonly executables.
		return script, script
	}
	return script, script
}

// scanTree walks an extracted skill directory and returns static findings. dir is
// the skill root (SKILL.md at its top). skillMD is the SKILL.md body (scanned for
// prompt-injection prose). Files larger than 512 KB are read capped.
func scanTree(dir, skillMD string) []ScanFinding {
	var findings []ScanFinding
	findings = append(findings, scanProse("SKILL.md", skillMD)...)

	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if rel == "SKILL.md" {
			return nil // already scanned as prose
		}
		scan, script := scannableText(rel)
		if !scan {
			return nil
		}
		content, rerr := readCapped(p, 512*1024)
		if rerr != nil {
			return nil
		}
		if script {
			findings = append(findings, applyRules(rel, content, scriptRules)...)
		}
		if strings.HasSuffix(strings.ToLower(rel), ".md") {
			findings = append(findings, scanProse(rel, content)...)
		}
		return nil
	})
	return dedupeFindings(findings)
}

func scanProse(file, body string) []ScanFinding {
	return applyRules(file, body, promptRules)
}

func applyRules(file, content string, rules []scanRule) []ScanFinding {
	var out []ScanFinding
	for _, r := range rules {
		if r.re.MatchString(content) {
			out = append(out, ScanFinding{File: file, Rule: r.rule, Severity: r.severity, Message: r.message})
		}
	}
	return out
}

func dedupeFindings(in []ScanFinding) []ScanFinding {
	seen := map[string]struct{}{}
	var out []ScanFinding
	for _, f := range in {
		key := f.File + "\x00" + f.Rule
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, f)
	}
	return out
}

func readCapped(p string, max int64) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, max)
	n, _ := f.Read(buf)
	return string(buf[:n]), nil
}
