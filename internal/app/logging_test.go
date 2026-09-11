package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolLogging(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	defer slog.SetDefault(old)
	for _, status := range []string{"success", "tool_error", "protocol_error"} {
		buf.Reset()
		handler := toolLogging(slog.Default(), func(context.Context, string, mcp.Request) (mcp.Result, error) {
			if status == "protocol_error" {
				return nil, errors.New("secret")
			}
			return &mcp.CallToolResult{IsError: status == "tool_error"}, nil
		})
		req := &mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Name: "remote_exec", Arguments: json.RawMessage(`{"server_id":"srv_test","command":"secret"}`)}}
		_, err := handler(context.Background(), "tools/call", req)
		if (err != nil) != (status == "protocol_error") {
			t.Fatal(err)
		}
		text := buf.String()
		for _, want := range []string{"tool_started", "tool_finished", "srv_test", "remote_exec", status, "duration_ms", "call_id"} {
			if !strings.Contains(text, want) {
				t.Fatalf("missing %s: %s", want, text)
			}
		}
		if strings.Contains(text, "secret") {
			t.Fatal("secret leaked")
		}
	}
}
