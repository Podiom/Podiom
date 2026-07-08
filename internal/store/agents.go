package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound reports that a requested store row does not exist.
var ErrNotFound = errors.New("not found")

// CreateAgent inserts a durable agent definition.
func (s *Store) CreateAgent(ctx context.Context, a Agent) (Agent, error) {
	fallback, err := json.Marshal(a.Fallback)
	if err != nil {
		return Agent{}, fmt.Errorf("encode fallback: %w", err)
	}
	mcpServers, err := json.Marshal(a.MCPServers)
	if err != nil {
		return Agent{}, fmt.Errorf("encode mcp servers: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO agents
		(name, provider, profile, model, effort, permission_mode, fallback_json, mcp_servers_json, mcp_config)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Name, a.Provider, a.Profile, a.Model, a.Effort, a.PermissionMode, string(fallback), string(mcpServers), a.MCPConfig,
	)
	if err != nil {
		return Agent{}, fmt.Errorf("create agent %q: %w", a.Name, err)
	}
	return s.GetAgent(ctx, a.Name)
}

// GetAgent fetches an agent by name.
func (s *Store) GetAgent(ctx context.Context, name string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, `SELECT
		name, provider, profile, model, effort, permission_mode, fallback_json, mcp_servers_json, mcp_config, avatar_updated_at, created_at, updated_at
		FROM agents WHERE name = ?`, name)
	a, err := scanAgent(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, fmt.Errorf("agent %q: %w", name, ErrNotFound)
		}
		return Agent{}, err
	}
	return a, nil
}

// ListAgents returns every agent ordered by name.
func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT
		name, provider, profile, model, effort, permission_mode, fallback_json, mcp_servers_json, mcp_config, avatar_updated_at, created_at, updated_at
		FROM agents ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// UpdateAgent replaces an agent's mutable defaults. The agent name is the key
// and is not renamed by this method.
func (s *Store) UpdateAgent(ctx context.Context, a Agent) (Agent, error) {
	fallback, err := json.Marshal(a.Fallback)
	if err != nil {
		return Agent{}, fmt.Errorf("encode fallback: %w", err)
	}
	mcpServers, err := json.Marshal(a.MCPServers)
	if err != nil {
		return Agent{}, fmt.Errorf("encode mcp servers: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET
		provider = ?, profile = ?, model = ?, effort = ?, permission_mode = ?,
		fallback_json = ?, mcp_servers_json = ?, mcp_config = ?, updated_at = datetime('now')
		WHERE name = ?`,
		a.Provider, a.Profile, a.Model, a.Effort, a.PermissionMode, string(fallback), string(mcpServers), a.MCPConfig, a.Name,
	)
	if err != nil {
		return Agent{}, fmt.Errorf("update agent %q: %w", a.Name, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return Agent{}, fmt.Errorf("update agent %q rows affected: %w", a.Name, err)
	}
	if changed == 0 {
		return Agent{}, fmt.Errorf("agent %q: %w", a.Name, ErrNotFound)
	}
	return s.GetAgent(ctx, a.Name)
}

// SetAgentAvatarUpdatedAt stamps (or clears, with ts == "") the version of an
// agent's uploaded profile picture. It is intentionally separate from
// UpdateAgent so generic edits to an agent's defaults never clobber the avatar.
func (s *Store) SetAgentAvatarUpdatedAt(ctx context.Context, name, ts string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET avatar_updated_at = ? WHERE name = ?`, ts, name)
	if err != nil {
		return fmt.Errorf("set avatar stamp for agent %q: %w", name, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set avatar stamp for agent %q rows affected: %w", name, err)
	}
	if changed == 0 {
		return fmt.Errorf("agent %q: %w", name, ErrNotFound)
	}
	return nil
}

// DeleteAgent removes an agent when no sessions still reference it.
func (s *Store) DeleteAgent(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE name = ?`, name)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "constraint failed") {
			return fmt.Errorf("delete agent %q: existing sessions still reference this agent; deletion would orphan session history: %w", name, err)
		}
		return fmt.Errorf("delete agent %q: %w", name, err)
	}
	changed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete agent %q rows affected: %w", name, err)
	}
	if changed == 0 {
		return fmt.Errorf("agent %q: %w", name, ErrNotFound)
	}
	return nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanAgent(row scanner) (Agent, error) {
	var a Agent
	var fallback, mcpServers string
	if err := row.Scan(
		&a.Name,
		&a.Provider,
		&a.Profile,
		&a.Model,
		&a.Effort,
		&a.PermissionMode,
		&fallback,
		&mcpServers,
		&a.MCPConfig,
		&a.AvatarUpdatedAt,
		&a.CreatedAt,
		&a.UpdatedAt,
	); err != nil {
		return Agent{}, err
	}
	if err := json.Unmarshal([]byte(fallback), &a.Fallback); err != nil {
		return Agent{}, fmt.Errorf("decode fallback for agent %q: %w", a.Name, err)
	}
	if strings.TrimSpace(mcpServers) != "" {
		if err := json.Unmarshal([]byte(mcpServers), &a.MCPServers); err != nil {
			return Agent{}, fmt.Errorf("decode mcp servers for agent %q: %w", a.Name, err)
		}
	}
	return a, nil
}
