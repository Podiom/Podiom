package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/creds"
	"github.com/Podiom/Podiom/internal/marketplace"
	"github.com/Podiom/Podiom/internal/store"
)

// executeAccessGrant runs the automatic grant for an approved access request
// and records the outcome (executed, or failed with the error — a failed
// request stays retryable in the UI). Acknowledge-only kinds (host-only
// cli_tool, env_var without a supplied value) terminate at approved: the user
// acts on the host themselves and the agent re-detects availability at its
// next review. An env_var approval carrying a secret value is fulfilled by
// Podiom: the credential is stored and injected into agent subprocess
// environments. The secret must never reach logs, the request row, or the
// timeline — only the variable name may appear in evidence.
func (s *Server) executeAccessGrant(ctx context.Context, req store.AccessRequest, secret string) store.AccessRequest {
	var payload map[string]string
	if err := json.Unmarshal([]byte(req.Payload), &payload); err != nil {
		payload = map[string]string{}
	}

	var execErr error
	var evidence string
	switch req.Kind {
	case store.AccessMCPServer:
		execErr = s.assignMCPServer(ctx, req.AgentName, payload["server_name"])
	case store.AccessSkill:
		execErr = s.installSkillGrant(ctx, payload)
	case store.AccessPermissionMode:
		execErr = s.setAgentPermissionMode(ctx, req.AgentName, payload["mode"])
	case store.AccessCLITool:
		// Acknowledge-only: the user installs the tool on the host themselves
		// and the agent re-detects it at its next review. Agents provision
		// their own tools through the shared toolset (podiom_install_tool),
		// which needs no approval, so a cli_tool request now means exactly one
		// thing — something only the user can install host-wide.
		return req
	case store.AccessEnvVar:
		if strings.TrimSpace(secret) == "" {
			// Acknowledge-only: the user sets the variable on the host
			// themselves, approved is terminal.
			return req
		}
		execErr = s.core.StoreCredential(ctx, creds.Credential{
			Name:    payload["var_name"],
			Value:   secret,
			Purpose: payload["purpose"],
			GoalID:  req.GoalID,
		})
		if execErr == nil {
			// Propagate the new credential into any long-lived provider process
			// (Codex app-server) so a running session picks it up without a
			// restart; Claude re-reads it on its next turn.
			s.core.RefreshCredentials()
			evidence = "Credential granted — " + payload["var_name"] + " is now set in the agent's environment"
		}
	default:
		execErr = fmt.Errorf("unknown access request kind %q", req.Kind)
	}

	msg := ""
	if execErr != nil {
		msg = execErr.Error()
	}
	marked, err := s.core.MarkAccessRequestExecuted(ctx, req.ID, msg, evidence)
	if err != nil {
		s.log.Warn("access grant bookkeeping failed", "event", "goal", "request", req.ID, "err", err)
		return req
	}
	return marked
}

// installSkillGrant installs the requested marketplace skill. Approving the
// request in the goal UI is the user's explicit consent, so the install is
// acknowledged.
func (s *Server) installSkillGrant(ctx context.Context, payload map[string]string) error {
	if s.marketplace == nil {
		return fmt.Errorf("skills marketplace is unavailable")
	}
	_, err := s.marketplace.Install(ctx, marketplace.InstallRequest{
		Registry:    marketplace.RegistryID(payload["registry"]),
		ID:          payload["id"],
		URL:         payload["url"],
		Acknowledge: true,
	})
	return err
}

// setAgentPermissionMode flips an agent's permission mode. The UI's approve
// dialog carries the yolo warning copy; by the time this runs the user has
// explicitly consented.
func (s *Server) setAgentPermissionMode(ctx context.Context, agentName, mode string) error {
	pm := config.PermissionMode(strings.TrimSpace(mode))
	if !config.KnownPermission(pm) {
		return fmt.Errorf("permission mode must be one of %s, got %q", config.PermissionModesLabel(), mode)
	}
	agent, err := s.core.GetAgent(ctx, agentName)
	if err != nil {
		return err
	}
	agent.PermissionMode = pm
	if _, err := s.core.UpdateAgent(ctx, agent); err != nil {
		return err
	}
	s.log.Info("permission mode granted", "event", "goal", "agent", agentName, "mode", string(pm))
	return nil
}
