package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Podiom/Podiom/internal/projects"
)

type projectAnalysisPayload struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Stack       []string `json:"stack"`
	Notes       string   `json:"notes"`
}

// AnalyzeProjectRepo asks a helper agent to inspect a downloaded repository
// snapshot and uses the compact JSON result to fill project metadata.
func (c *Core) AnalyzeProjectRepo(ctx context.Context, id, agentName string) (projects.Project, error) {
	if err := c.syncProjectRoadmaps(ctx); err != nil {
		return projects.Project{}, err
	}
	proj, err := c.ledger.Get(id)
	if err != nil {
		return projects.Project{}, err
	}
	if proj.Repo == nil {
		return projects.Project{}, fmt.Errorf("project %q has no connected repo", id)
	}
	agent, err := c.helperAgent(ctx, agentName, "")
	if err != nil {
		return projects.Project{}, err
	}

	repoRoot := filepath.Join(c.paths.ProjectsDir, proj.Path, "repo")
	prompt := "Analyze the downloaded GitHub repository snapshot at " + repoRoot + ". " +
		"Inspect only README files, top-level manifests such as package.json, go.mod, pyproject.toml, Cargo.toml, pom.xml, build.gradle, composer.json, Gemfile, requirements.txt, and the top-level directory listing. " +
		"Do not read the whole repository. Return only compact JSON with this exact shape and no markdown fences: " +
		`{"name":"max 6 words","description":"one sentence, max 160 chars","stack":["up to 8 techs"],"notes":"2-4 sentences: purpose, layout, build/run/test"}.`

	raw := c.oneShotCompletionWithOptions(ctx, agent, prompt, oneShotOptions{
		ExtraWorkspaceDirs: []string{repoRoot},
		Unattended:         true,
		AllowedTools:       []string{"Read", "Glob", "Grep"},
	})
	payload, err := parseProjectAnalysis(raw)
	if err != nil {
		return projects.Project{}, err
	}
	if payload.Name == "" {
		return projects.Project{}, fmt.Errorf("the model returned no project name")
	}

	patch := projects.ProjectPatch{
		Name:  &payload.Name,
		Stack: &payload.Stack,
		Notes: &payload.Notes,
	}
	if payload.Description != "" {
		patch.Description = &payload.Description
	}
	return c.UpdateProject(ctx, id, patch)
}

func parseProjectAnalysis(raw string) (projectAnalysisPayload, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	var payload projectAnalysisPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return projectAnalysisPayload{}, fmt.Errorf("parse project analysis: %w", err)
	}
	payload.Name = truncateWords(strings.TrimSpace(payload.Name), 6)
	payload.Description = truncateRunes(strings.TrimSpace(payload.Description), 200)
	payload.Notes = strings.TrimSpace(payload.Notes)
	stack := make([]string, 0, len(payload.Stack))
	for _, tech := range payload.Stack {
		tech = strings.TrimSpace(tech)
		if tech == "" {
			continue
		}
		stack = append(stack, tech)
		if len(stack) == 8 {
			break
		}
	}
	payload.Stack = stack
	return payload, nil
}
