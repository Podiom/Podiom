package main

import (
	"net/http"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
)

// helperToken resolves the gateway token once for the MCP helper subprocesses
// (permission-mcp / plan-mcp / manage-mcp). They receive PODIOM_HOME from the
// generated internal MCP config, so the disk read resolves the same token the
// daemon enforces (HA7). Read on every callback instead of caching: Codex MCP
// helpers can be long-lived, and token rotation must take effect immediately.
func helperToken() string {
	home, err := config.ResolveHome()
	if err != nil {
		return ""
	}
	token, err := gateway.ReadTokenFile(config.NewPaths(home).GatewayToken)
	if err != nil {
		return ""
	}
	return token
}

// setGatewayToken attaches the gateway token to a daemon-callback request.
func setGatewayToken(req *http.Request) {
	if token := helperToken(); token != "" {
		req.Header.Set(gateway.Header, token)
	}
}
