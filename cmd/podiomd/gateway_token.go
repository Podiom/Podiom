package main

import (
	"net/http"
	"sync"

	"github.com/Podiom/Podiom/internal/config"
	"github.com/Podiom/Podiom/internal/gateway"
)

// helperToken resolves the gateway token once for the MCP helper subprocesses
// (permission-mcp / plan-mcp). They inherit PODIOM_HOME from the daemon that
// spawned them, so the disk read resolves the same token the daemon enforces
// (HA7); best-effort so helpers still work against a token-less daemon.
var helperToken = sync.OnceValue(func() string {
	home, err := config.ResolveHome()
	if err != nil {
		return ""
	}
	token, err := gateway.ReadTokenFile(config.NewPaths(home).GatewayToken)
	if err != nil {
		return ""
	}
	return token
})

// setGatewayToken attaches the gateway token to a daemon-callback request.
func setGatewayToken(req *http.Request) {
	if token := helperToken(); token != "" {
		req.Header.Set(gateway.Header, token)
	}
}
