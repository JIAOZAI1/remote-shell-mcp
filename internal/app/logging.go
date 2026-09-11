package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// toolLogging also covers unknown tools and argument validation failures.
// Never log raw arguments, output or error messages: any can contain secrets.
func toolLogging(base *slog.Logger, next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		name, serverID := "", ""
		if p, ok := req.GetParams().(*mcp.CallToolParamsRaw); ok && p != nil {
			name = p.Name
			var target struct {
				ServerID string `json:"server_id"`
			}
			if json.Unmarshal(p.Arguments, &target) == nil {
				serverID = target.ServerID
			}
		}
		logger := base.With("call_id", rand.Text(), "tool", name, "server_id", serverID)
		start := time.Now()
		logger.InfoContext(ctx, "tool_started")
		completed := false
		defer func() {
			status := "success"
			if !completed {
				status = "aborted"
			} else if err != nil {
				status = "protocol_error"
			} else if r, ok := result.(*mcp.CallToolResult); ok && r != nil && r.IsError {
				status = "tool_error"
			}
			logger.InfoContext(ctx, "tool_finished", "status", status, "duration_ms", time.Since(start).Milliseconds())
		}()
		result, err = next(ctx, method, req)
		completed = true
		return result, err
	}
}
