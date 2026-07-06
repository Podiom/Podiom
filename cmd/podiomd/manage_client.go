package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// manageClient is a thin HTTP helper the manage-mcp tools use to call the
// daemon's own REST API over loopback. Every request carries the gateway token
// (setGatewayToken), matching the plan-mcp / permission-mcp callback pattern.
type manageClient struct {
	base string
	http *http.Client
}

func newManageClient(addr string) *manageClient {
	return &manageClient{base: "http://" + addr, http: http.DefaultClient}
}

// do issues a request and returns the response body. Non-2xx responses become an
// error carrying the daemon's own error text so the agent sees why a call failed.
func (c *manageClient) do(ctx context.Context, method, path string, body any) (string, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return "", err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	setGatewayToken(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("podiom API %s %s -> status %d: %s", method, path, resp.StatusCode, bytes.TrimSpace(raw))
	}
	return prettyJSON(raw), nil
}

func (c *manageClient) get(ctx context.Context, path string) (string, error) {
	return c.do(ctx, http.MethodGet, path, nil)
}

func (c *manageClient) post(ctx context.Context, path string, body any) (string, error) {
	return c.do(ctx, http.MethodPost, path, body)
}

func (c *manageClient) patch(ctx context.Context, path string, body any) (string, error) {
	return c.do(ctx, http.MethodPatch, path, body)
}

func (c *manageClient) put(ctx context.Context, path string, body any) (string, error) {
	return c.do(ctx, http.MethodPut, path, body)
}

func (c *manageClient) del(ctx context.Context, path string) (string, error) {
	return c.do(ctx, http.MethodDelete, path, nil)
}

// prettyJSON re-indents a JSON response for readability in the tool result. If
// the body isn't JSON (or is empty), it's returned verbatim.
func prettyJSON(raw []byte) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "ok"
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return string(raw)
	}
	return buf.String()
}
