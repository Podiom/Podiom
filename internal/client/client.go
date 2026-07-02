// Package client is the thin transport the `podium` CLI uses to talk to a
// running podiumd over HTTP. The CLI never runs sessions in-process — it is
// always a client of the daemon (R11.1 / D2).
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mar-schmidt/Podium/internal/adapter"
	"github.com/mar-schmidt/Podium/internal/config"
	podiummcp "github.com/mar-schmidt/Podium/internal/mcp"
	"github.com/mar-schmidt/Podium/internal/projects"
	"github.com/mar-schmidt/Podium/internal/schedule"
	"github.com/mar-schmidt/Podium/internal/server"
	"github.com/mar-schmidt/Podium/internal/store"
	"github.com/mar-schmidt/Podium/internal/updater"
	"github.com/mar-schmidt/Podium/internal/usage"
)

// ErrDaemonUnreachable indicates podiumd is not accepting connections at the
// configured address (most commonly: it isn't running).
var ErrDaemonUnreachable = errors.New("podiumd is not reachable")

// Client talks to podiumd at a base URL like http://127.0.0.1:8787.
type Client struct {
	baseURL string
	http    *http.Client
}

// New returns a client for the given host:port.
func New(addr string) *Client {
	return &Client{
		baseURL: "http://" + addr,
		http:    &http.Client{Timeout: 5 * time.Second},
	}
}

// Health fetches /healthz. It maps connection refusals to ErrDaemonUnreachable so
// the CLI can print a helpful "start the daemon" message instead of a raw error.
func (c *Client) Health(ctx context.Context) (server.Health, error) {
	var h server.Health
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/healthz", nil)
	if err != nil {
		return h, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		var netErr *net.OpError
		if errors.As(err, &netErr) {
			return h, fmt.Errorf("%w at %s", ErrDaemonUnreachable, c.baseURL)
		}
		return h, fmt.Errorf("%w: %v", ErrDaemonUnreachable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return h, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, c.baseURL)
	}
	if err := json.NewDecoder(resp.Body).Decode(&h); err != nil {
		return h, fmt.Errorf("decode health: %w", err)
	}
	return h, nil
}

// AgentCreateRequest is the CLI transport shape for POST /api/agents.
type AgentCreateRequest struct {
	Name           string                `json:"name"`
	Provider       config.Provider       `json:"provider,omitempty"`
	Profile        string                `json:"profile,omitempty"`
	Model          string                `json:"model,omitempty"`
	Effort         string                `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	Fallback       []string              `json:"fallback,omitempty"`
	MCPServers     []string              `json:"mcp_servers,omitempty"`
}

// ProfileRequest is the transport shape for creating/updating auth profiles.
type ProfileRequest struct {
	Name      string          `json:"name,omitempty"`
	Provider  config.Provider `json:"provider,omitempty"`
	ConfigDir string          `json:"config_dir,omitempty"`
	HomeDir   string          `json:"home_dir,omitempty"`
}

// ListProfiles lists configured auth profiles from the daemon.
func (c *Client) ListProfiles(ctx context.Context) ([]config.Profile, error) {
	var profiles []config.Profile
	if err := c.getJSON(ctx, "/api/profiles", &profiles); err != nil {
		return nil, err
	}
	return profiles, nil
}

// CreateProfile creates an auth profile through the daemon.
func (c *Client) CreateProfile(ctx context.Context, req ProfileRequest) (config.Profile, error) {
	var profile config.Profile
	if err := c.postJSON(ctx, "/api/profiles", req, &profile); err != nil {
		return profile, err
	}
	return profile, nil
}

// UpdateProfile updates a configured auth profile through the daemon.
func (c *Client) UpdateProfile(ctx context.Context, name string, req ProfileRequest) (config.Profile, error) {
	var profile config.Profile
	if err := c.putJSON(ctx, "/api/profiles/"+urlPathEscape(name), req, &profile); err != nil {
		return profile, err
	}
	return profile, nil
}

// DeleteProfile deletes a configured auth profile through the daemon.
func (c *Client) DeleteProfile(ctx context.Context, name string) error {
	return c.deleteJSON(ctx, "/api/profiles/"+urlPathEscape(name), nil, nil)
}

// CreateAgent creates an agent through the daemon.
func (c *Client) CreateAgent(ctx context.Context, req AgentCreateRequest) (store.Agent, error) {
	var agent store.Agent
	if err := c.postJSON(ctx, "/api/agents", req, &agent); err != nil {
		return agent, err
	}
	return agent, nil
}

// AgentDetail bundles an agent with its editable SOUL.md body.
type AgentDetail struct {
	store.Agent
	MCPServers []string `json:"MCPServers"`
	Soul       string   `json:"Soul"`
}

// GetAgent fetches an agent and its SOUL.md body.
func (c *Client) GetAgent(ctx context.Context, name string) (AgentDetail, error) {
	var detail AgentDetail
	if err := c.getJSON(ctx, "/api/agents/"+urlPathEscape(name), &detail); err != nil {
		return detail, err
	}
	return detail, nil
}

// AgentUpdateRequest updates mutable agent defaults and optionally SOUL.md.
type AgentUpdateRequest struct {
	Provider       config.Provider       `json:"provider,omitempty"`
	Profile        *string               `json:"profile,omitempty"`
	Model          *string               `json:"model,omitempty"`
	Effort         *string               `json:"effort,omitempty"`
	PermissionMode config.PermissionMode `json:"permission_mode,omitempty"`
	Fallback       *[]string             `json:"fallback,omitempty"`
	MCPServers     *[]string             `json:"mcp_servers,omitempty"`
	Soul           *string               `json:"soul,omitempty"`
}

type MCPSnapshot struct {
	Servers     []podiummcp.Server  `json:"servers"`
	Agents      []MCPAgent          `json:"agents"`
	Assignments map[string][]string `json:"assignments"`
}

type MCPAgent struct {
	Name       string   `json:"name"`
	Provider   string   `json:"provider"`
	MCPServers []string `json:"mcp_servers"`
}

type MCPAssignmentRequest struct {
	AgentName  string `json:"agent_name"`
	ServerName string `json:"server_name"`
	Assigned   bool   `json:"assigned"`
}

func (c *Client) MCPSnapshot(ctx context.Context) (MCPSnapshot, error) {
	var snapshot MCPSnapshot
	if err := c.getJSON(ctx, "/api/mcp", &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (c *Client) UpsertMCPServer(ctx context.Context, server podiummcp.Server) (MCPSnapshot, error) {
	var snapshot MCPSnapshot
	if err := c.postJSON(ctx, "/api/mcp/servers", server, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (c *Client) RemoveMCPServer(ctx context.Context, name string) (MCPSnapshot, error) {
	var snapshot MCPSnapshot
	if err := c.deleteJSON(ctx, "/api/mcp/servers/"+urlPathEscape(name), nil, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

func (c *Client) SetMCPAssignment(ctx context.Context, req MCPAssignmentRequest) (MCPSnapshot, error) {
	var snapshot MCPSnapshot
	if err := c.putJSON(ctx, "/api/mcp/assignments", req, &snapshot); err != nil {
		return snapshot, err
	}
	return snapshot, nil
}

// UpdateAgent updates an agent through the daemon.
func (c *Client) UpdateAgent(ctx context.Context, name string, req AgentUpdateRequest) (AgentDetail, error) {
	var detail AgentDetail
	if err := c.putJSON(ctx, "/api/agents/"+urlPathEscape(name), req, &detail); err != nil {
		return detail, err
	}
	return detail, nil
}

// AgentDeleteResult is the DELETE /api/agents/<name> response.
type AgentDeleteResult struct {
	ArchivePath      string `json:"archive_path,omitempty"`
	ArchivedSessions int    `json:"archived_sessions"`
}

// DeleteAgent deletes an agent after server-side name confirmation.
func (c *Client) DeleteAgent(ctx context.Context, name, confirmation string) (AgentDeleteResult, error) {
	var result AgentDeleteResult
	if err := c.deleteJSON(ctx, "/api/agents/"+urlPathEscape(name), map[string]string{"confirmation": confirmation}, &result); err != nil {
		return result, err
	}
	return result, nil
}

// MemoryInfo is the GET/PUT /api/agents/<name>/memory response: the agent's
// MEMORY.md plus its dream status.
type MemoryInfo struct {
	Agent           string       `json:"agent"`
	Memory          string       `json:"memory"`
	Lines           int          `json:"lines"`
	BudgetLines     int          `json:"budget_lines"`
	PendingSessions int          `json:"pending_sessions"`
	LastDream       *store.Dream `json:"last_dream"`
}

// MemoryStatusRow is one agent's line in GET /api/memory/status.
type MemoryStatusRow struct {
	Agent           string       `json:"agent"`
	PendingSessions int          `json:"pending_sessions"`
	MemoryLines     int          `json:"memory_lines"`
	BudgetLines     int          `json:"budget_lines"`
	LastDream       *store.Dream `json:"last_dream"`
}

// DreamResult is the POST /api/agents/<name>/dream response.
type DreamResult struct {
	NoOp  bool         `json:"noop"`
	Dream *store.Dream `json:"dream"`
}

// GetMemory fetches an agent's MEMORY.md and dream status.
func (c *Client) GetMemory(ctx context.Context, name string) (MemoryInfo, error) {
	var info MemoryInfo
	if err := c.getJSON(ctx, "/api/agents/"+urlPathEscape(name)+"/memory", &info); err != nil {
		return info, err
	}
	return info, nil
}

// PutMemory overwrites an agent's MEMORY.md with the given content.
func (c *Client) PutMemory(ctx context.Context, name, memory string) (MemoryInfo, error) {
	var info MemoryInfo
	if err := c.putJSON(ctx, "/api/agents/"+urlPathEscape(name)+"/memory", map[string]string{"memory": memory}, &info); err != nil {
		return info, err
	}
	return info, nil
}

// ClearMemory empties an agent's MEMORY.md.
func (c *Client) ClearMemory(ctx context.Context, name string) (MemoryInfo, error) {
	var info MemoryInfo
	if err := c.deleteJSON(ctx, "/api/agents/"+urlPathEscape(name)+"/memory", nil, &info); err != nil {
		return info, err
	}
	return info, nil
}

// Dream triggers a memory-consolidation dream on demand.
func (c *Client) Dream(ctx context.Context, name string) (DreamResult, error) {
	var result DreamResult
	if err := c.postJSON(ctx, "/api/agents/"+urlPathEscape(name)+"/dream", nil, &result); err != nil {
		return result, err
	}
	return result, nil
}

// ListDreams fetches an agent's dream journal, newest first.
func (c *Client) ListDreams(ctx context.Context, name string, limit int) ([]store.Dream, error) {
	path := "/api/agents/" + urlPathEscape(name) + "/dreams"
	if limit > 0 {
		path += "?limit=" + strconv.Itoa(limit)
	}
	var dreams []store.Dream
	if err := c.getJSON(ctx, path, &dreams); err != nil {
		return nil, err
	}
	return dreams, nil
}

// MemoryStatus fetches the fleet-wide per-agent memory summary.
func (c *Client) MemoryStatus(ctx context.Context) ([]MemoryStatusRow, error) {
	var rows []MemoryStatusRow
	if err := c.getJSON(ctx, "/api/memory/status", &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// ListAgents lists agents from the daemon.
func (c *Client) ListAgents(ctx context.Context) ([]store.Agent, error) {
	var agents []store.Agent
	if err := c.getJSON(ctx, "/api/agents", &agents); err != nil {
		return nil, err
	}
	return agents, nil
}

// SessionCreateRequest creates a session with explicit origin.
type SessionCreateRequest struct {
	AgentName string              `json:"agent_name"`
	Origin    store.SessionOrigin `json:"origin"`
	Provider  config.Provider     `json:"provider,omitempty"`
	Profile   string              `json:"profile,omitempty"`
}

// CreateSession creates a durable session through the daemon.
func (c *Client) CreateSession(ctx context.Context, req SessionCreateRequest) (store.Session, error) {
	var session store.Session
	if err := c.postJSON(ctx, "/api/sessions", req, &session); err != nil {
		return session, err
	}
	return session, nil
}

// ListSessions fetches all durable sessions from the daemon.
func (c *Client) ListSessions(ctx context.Context) ([]store.Session, error) {
	var sessions []store.Session
	if err := c.getJSON(ctx, "/api/sessions", &sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// DeleteSession removes a durable session and its history through the daemon.
func (c *Client) DeleteSession(ctx context.Context, id string) error {
	return c.deleteJSON(ctx, "/api/sessions/"+urlPathEscape(id), nil, nil)
}

// ChatRequest sends one message, either to an existing session or to a new
// session created from AgentName.
type ChatRequest struct {
	SessionID string          `json:"session_id,omitempty"`
	AgentName string          `json:"agent_name,omitempty"`
	Message   string          `json:"message"`
	Provider  config.Provider `json:"provider,omitempty"`
	Profile   string          `json:"profile,omitempty"`
}

// StreamEvent is one newline-delimited event from /api/chat.
type StreamEvent struct {
	Type    string                     `json:"type"`
	Session *store.Session             `json:"session,omitempty"`
	Message *store.Message             `json:"message,omitempty"`
	Delta   string                     `json:"delta,omitempty"`
	Notice  string                     `json:"notice,omitempty"`
	Request *adapter.PermissionRequest `json:"request,omitempty"`
	Input   *adapter.UserInputRequest  `json:"input,omitempty"`
	Error   string                     `json:"error,omitempty"`
}

// Chat streams one turn from the daemon.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (<-chan StreamEvent, <-chan error) {
	events := make(chan StreamEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		raw, _ := json.Marshal(req)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/chat", bytes.NewReader(raw))
		if err != nil {
			errs <- err
			return
		}
		httpReq.Header.Set("Content-Type", "application/json")
		httpClient := &http.Client{}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			errs <- err
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			errs <- fmt.Errorf("chat status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
			return
		}
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			var event StreamEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				errs <- err
				return
			}
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case events <- event:
			}
		}
		if err := scanner.Err(); err != nil {
			errs <- err
		}
	}()
	return events, errs
}

// DecidePermission sends an allow/deny decision for a pending permission
// request.
func (c *Client) DecidePermission(ctx context.Context, id string, decision adapter.PermissionDecision) error {
	return c.postJSON(ctx, "/api/permission-decisions/"+id, decision, nil)
}

// DecideUserInput sends answers for a pending provider clarification request.
func (c *Client) DecideUserInput(ctx context.Context, id string, decision adapter.UserInputDecision) error {
	return c.postJSON(ctx, "/api/user-input-decisions/"+id, decision, nil)
}

// ListSchedules fetches schedule status (next run + recent run history) from the
// daemon.
func (c *Client) ListSchedules(ctx context.Context) ([]schedule.Status, error) {
	var statuses []schedule.Status
	if err := c.getJSON(ctx, "/api/schedules", &statuses); err != nil {
		return nil, err
	}
	return statuses, nil
}

// RunSchedule triggers a manual run and returns the recorded run. The run
// executes a full agent turn, so this uses a client without the short default
// timeout.
func (c *Client) RunSchedule(ctx context.Context, name string) (store.ScheduleRun, error) {
	var run store.ScheduleRun
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/schedules/"+name+"/run", nil)
	if err != nil {
		return run, err
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return run, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return run, fmt.Errorf("run schedule %q status %d: %s", name, resp.StatusCode, bytes.TrimSpace(body))
	}
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return run, err
	}
	return run, nil
}

// DeleteSchedule removes a schedule file and its run history through the daemon.
func (c *Client) DeleteSchedule(ctx context.Context, name string) error {
	return c.deleteJSON(ctx, "/api/schedules/"+urlPathEscape(name), nil, nil)
}

// ListProjects fetches the shared project ledger from the daemon.
func (c *Client) ListProjects(ctx context.Context) ([]projects.Project, error) {
	var list []projects.Project
	if err := c.getJSON(ctx, "/api/projects", &list); err != nil {
		return nil, err
	}
	return list, nil
}

// Usage fetches per-profile provider usage snapshots. When refresh is true it
// asks the daemon to force a live re-fetch; because that can take longer than
// the client's default 5s timeout, the refresh path uses its own request client.
func (c *Client) Usage(ctx context.Context, refresh bool) ([]usage.Snapshot, error) {
	path := "/api/usage"
	if !refresh {
		var snaps []usage.Snapshot
		if err := c.getJSON(ctx, path, &snaps); err != nil {
			return nil, err
		}
		return snaps, nil
	}
	path += "?refresh=1"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: 20 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("GET %s status %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	var snaps []usage.Snapshot
	if err := json.NewDecoder(resp.Body).Decode(&snaps); err != nil {
		return nil, err
	}
	return snaps, nil
}

// ListTasks fetches all roadmap tasks from the daemon.
func (c *Client) ListTasks(ctx context.Context) ([]store.Task, error) {
	var tasks []store.Task
	if err := c.getJSON(ctx, "/api/tasks", &tasks); err != nil {
		return nil, err
	}
	return tasks, nil
}

// DeleteTask removes a roadmap task through the daemon. Sessions started from the
// task are preserved.
func (c *Client) DeleteTask(ctx context.Context, id string) error {
	return c.deleteJSON(ctx, "/api/tasks/"+urlPathEscape(id), nil, nil)
}

// ArchiveDoneResult is the POST /api/tasks/archive-done response.
type ArchiveDoneResult struct {
	ArchivePath      string `json:"archive_path,omitempty"`
	ArchivedTasks    int    `json:"archived_tasks"`
	ArchivedSessions int    `json:"archived_sessions"`
}

// ArchiveDoneTasks archives every done task (and its sessions) to disk and
// removes them from the active app through the daemon.
func (c *Client) ArchiveDoneTasks(ctx context.Context) (ArchiveDoneResult, error) {
	var result ArchiveDoneResult
	if err := c.postJSON(ctx, "/api/tasks/archive-done", map[string]string{}, &result); err != nil {
		return result, err
	}
	return result, nil
}

// UpdateApplyRequest starts an update through the daemon.
type UpdateApplyRequest struct {
	Version string `json:"version,omitempty"`
	Force   bool   `json:"force,omitempty"`
}

// CheckUpdate checks GitHub Releases through the daemon.
func (c *Client) CheckUpdate(ctx context.Context) (updater.Status, error) {
	var status updater.Status
	if err := c.getJSON(ctx, "/api/update", &status); err != nil {
		return status, err
	}
	return status, nil
}

// ApplyUpdate starts a daemon-coordinated update.
func (c *Client) ApplyUpdate(ctx context.Context, req UpdateApplyRequest) (updater.ApplyResult, error) {
	var result updater.ApplyResult
	if err := c.postJSON(ctx, "/api/update/apply", req, &result); err != nil {
		return result, err
	}
	return result, nil
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GET %s status %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) postJSON(ctx context.Context, path string, in any, out any) error {
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("POST %s status %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) putJSON(ctx context.Context, path string, in any, out any) error {
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("PUT %s status %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func (c *Client) deleteJSON(ctx context.Context, path string, in any, out any) error {
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("DELETE %s status %d: %s", path, resp.StatusCode, bytes.TrimSpace(body))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func urlPathEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}
