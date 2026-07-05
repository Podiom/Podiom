package marketplace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// buildDetail resolves a skill's canonical GitHub location to a full inspection
// payload (FR-7..10). It pins the ref to a commit SHA (SEC-3), downloads the
// subtree into a temp dir, walks it for the file tree + sizes + executable bits,
// reads SKILL.md, parses ordered frontmatter, detects a license, and runs the
// static heuristic scan (SEC-6). The temp dir is removed before returning —
// individual files are re-fetched on demand by the file-inspection endpoint.
//
// base carries the registry-supplied metadata (name/desc/stars/verified/…);
// files always come from GitHub at the pinned SHA regardless of registry.
func buildDetail(ctx context.Context, gh *ghFetcher, ref SkillRef, base SkillSummary) (SkillDetail, error) {
	if ref.SHA == "" {
		sha, err := gh.resolveSHA(ctx, ref.Owner, ref.Repo, firstNonEmpty(ref.SHA, "HEAD"))
		if err != nil {
			return SkillDetail{}, err
		}
		ref.SHA = sha
	}
	base.Ref = ref

	zip, err := gh.downloadZip(ctx, ref)
	if err != nil {
		return SkillDetail{}, err
	}
	tmp, err := os.MkdirTemp("", "podiom-skill-detail-*")
	if err != nil {
		return SkillDetail{}, err
	}
	defer os.RemoveAll(tmp)

	rd, size := gh.zipReader(zip)
	if err := extractSubtree(rd, size, ref.Path, tmp, gh.maxSize); err != nil {
		return SkillDetail{}, err
	}
	return assembleDetail(tmp, base)
}

// assembleDetail builds a SkillDetail from an already-extracted skill directory
// (skill root, SKILL.md at top). Shared by buildDetail and install validation.
func assembleDetail(dir string, base SkillSummary) (SkillDetail, error) {
	skillMDPath := filepath.Join(dir, "SKILL.md")
	raw, err := os.ReadFile(skillMDPath)
	if err != nil {
		return SkillDetail{}, fmt.Errorf("SKILL.md missing at skill root: %w", err)
	}
	skillMD := string(raw)
	fields, name, desc := parseFrontmatterFields(skillMD)
	if base.Name == "" {
		base.Name = name
	}
	if base.Description == "" {
		base.Description = desc
	}

	tree, total, hasExec, license := walkSkill(dir)
	base.HasScripts = hasExec
	detail := SkillDetail{
		SkillSummary:  base,
		Frontmatter:   fields,
		SkillMD:       skillMD,
		Tree:          tree,
		License:       license,
		HasExecutable: hasExec,
		Size:          total,
		ScanFindings:  scanTree(dir, skillMD),
	}
	if detail.ScanFindings == nil {
		detail.ScanFindings = []ScanFinding{}
	}
	return detail, nil
}

// walkSkill produces the file tree (FR-7), total size, whether any executable
// content is present (FR-9), and a detected license name.
func walkSkill(dir string) (tree []FileNode, total int64, hasExec bool, license string) {
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil || rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		node := FileNode{Path: rel, IsDir: d.IsDir()}
		if !d.IsDir() {
			node.Size = info.Size()
			total += info.Size()
			node.Executable = isExecutable(rel, info.Mode().Perm())
			if node.Executable {
				hasExec = true
			}
			if l := licenseName(rel); l != "" && license == "" {
				license = l
			}
		}
		tree = append(tree, node)
		return nil
	})
	sort.Slice(tree, func(i, j int) bool { return tree[i].Path < tree[j].Path })
	return tree, total, hasExec, license
}

// isExecutable flags content agents may run: an executable permission bit, a
// scripts/ location, or a known script extension (FR-9).
func isExecutable(rel string, mode os.FileMode) bool {
	if mode&0o111 != 0 {
		return true
	}
	lower := strings.ToLower(rel)
	if strings.HasPrefix(lower, "scripts/") || strings.Contains(lower, "/scripts/") {
		return true
	}
	switch filepath.Ext(lower) {
	case ".sh", ".bash", ".zsh", ".py", ".js", ".ts", ".mjs", ".cjs", ".rb", ".pl", ".ps1":
		return true
	}
	return false
}

func licenseName(rel string) string {
	base := strings.ToUpper(filepath.Base(rel))
	if base == "LICENSE" || base == "LICENSE.MD" || base == "LICENSE.TXT" || base == "COPYING" {
		return "See " + filepath.Base(rel)
	}
	return ""
}

// parseFrontmatterFields extracts the leading `--- ... ---` YAML block as an
// ordered slice of key/value pairs (stable render, FR-7/10), plus name+desc for
// convenience. Non-scalar values are rendered compactly.
func parseFrontmatterFields(body string) (fields []FrontmatterField, name, desc string) {
	s := strings.TrimLeft(strings.TrimPrefix(body, "\ufeff"), " \t\r\n")
	if !strings.HasPrefix(s, "---") {
		return nil, "", ""
	}
	rest := s[len("---"):]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return nil, "", ""
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(rest[:idx]), &doc); err != nil {
		return nil, "", ""
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, "", ""
	}
	m := doc.Content[0]
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := strings.TrimSpace(m.Content[i].Value)
		val := scalarValue(m.Content[i+1])
		fields = append(fields, FrontmatterField{Key: key, Value: val})
		switch strings.ToLower(key) {
		case "name":
			name = strings.TrimSpace(val)
		case "description":
			desc = strings.TrimSpace(val)
		}
	}
	return fields, name, desc
}

func scalarValue(n *yaml.Node) string {
	switch n.Kind {
	case yaml.ScalarNode:
		return strings.TrimSpace(n.Value)
	case yaml.SequenceNode:
		var parts []string
		for _, c := range n.Content {
			parts = append(parts, scalarValue(c))
		}
		return strings.Join(parts, ", ")
	case yaml.MappingNode:
		var parts []string
		for i := 0; i+1 < len(n.Content); i += 2 {
			parts = append(parts, n.Content[i].Value+": "+scalarValue(n.Content[i+1]))
		}
		return strings.Join(parts, ", ")
	}
	return strings.TrimSpace(n.Value)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
