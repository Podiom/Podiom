package adapter

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Podiom/Podiom/internal/capabilities"
	"github.com/Podiom/Podiom/internal/claudeauth"
)

// claudeModelsURL is the Anthropic REST endpoint that enumerates the models the
// authenticated account may use. It is a package var so tests can point it at an
// httptest server. The Claude Code CLI itself exposes no model listing, so this
// OAuth-scoped API call is the only dynamic source (see claude.go Capabilities).
var claudeModelsURL = "https://api.anthropic.com/v1/models"

// claudeAPIVersion is the anthropic-version header required by api.anthropic.com.
const claudeAPIVersion = "2023-06-01"

// claudeModelsClient is shared and honours the caller's context deadline; the
// timeout here is only a backstop for a caller that forgot to set one.
var claudeModelsClient = &http.Client{Timeout: 10 * time.Second}

// fetchClaudeModels lists the account's available Claude models via the Anthropic
// REST API, authenticated with the profile's OAuth token (the same credential the
// usage fetch uses). It paginates until has_more is false and returns the models
// as-is; efforts are merged separately by the caller. Any failure (no token,
// non-200, decode error) is returned so the caller can fall back to the bundled
// catalogue.
func fetchClaudeModels(ctx context.Context, configDir string) ([]capabilities.ModelOption, error) {
	token, err := claudeauth.AccessToken(configDir)
	if err != nil {
		return nil, fmt.Errorf("claude models: %w", err)
	}

	var all []capabilities.ModelOption
	afterID := ""
	for {
		endpoint := claudeModelsURL
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		endpoint += "?" + params.Encode()

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("claude models: build request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-beta", claudeauth.OAuthBeta)
		req.Header.Set("anthropic-version", claudeAPIVersion)
		req.Header.Set("User-Agent", claudeauth.UserAgent)

		resp, err := claudeModelsClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("claude models: request: %w", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("claude models: unexpected status %d", resp.StatusCode)
		}
		if readErr != nil {
			return nil, fmt.Errorf("claude models: read body: %w", readErr)
		}

		page, hasMore, lastID, err := parseClaudeModelList(body)
		if err != nil {
			return nil, fmt.Errorf("claude models: %w", err)
		}
		all = append(all, page...)
		if !hasMore || lastID == "" {
			break
		}
		afterID = lastID
	}

	if len(all) == 0 {
		return nil, fmt.Errorf("claude models: empty list")
	}
	markClaudeDefault(all)
	return all, nil
}

// parseClaudeModelList decodes one page of GET /v1/models. It returns the page's
// models, the has_more flag, and last_id for cursor pagination. Entries with an
// empty id are skipped. display_name falls back to id when absent.
func parseClaudeModelList(raw []byte) (models []capabilities.ModelOption, hasMore bool, lastID string, err error) {
	var resp struct {
		Data []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"data"`
		HasMore bool   `json:"has_more"`
		LastID  string `json:"last_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, false, "", err
	}
	out := make([]capabilities.ModelOption, 0, len(resp.Data))
	for _, item := range resp.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		display := strings.TrimSpace(item.DisplayName)
		if display == "" {
			display = id
		}
		out = append(out, capabilities.ModelOption{
			ID:          id,
			Model:       id,
			DisplayName: display,
			Description: display + ".",
		})
	}
	return out, resp.HasMore, resp.LastID, nil
}

// markClaudeDefault flags a sensible default when the API does not designate one:
// the first Opus-family model, else the first entry. No-op on an empty list or if
// a default is already set.
func markClaudeDefault(models []capabilities.ModelOption) {
	if len(models) == 0 {
		return
	}
	for _, m := range models {
		if m.IsDefault {
			return
		}
	}
	for i := range models {
		if strings.Contains(strings.ToLower(models[i].Model), "opus") {
			models[i].IsDefault = true
			return
		}
	}
	models[0].IsDefault = true
}
