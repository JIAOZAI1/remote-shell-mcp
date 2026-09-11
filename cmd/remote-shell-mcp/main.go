package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"

	"remote-shell-mcp/internal/app"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
	dir, err := os.UserHomeDir()
	if err != nil {
		slog.Error("resolve user home directory")
		os.Exit(1)
	}
	config := flag.String("config", filepath.Join(dir, ".remote-shell-mcp", "config.json"), "local configuration file")
	flag.Parse()
	if err = run(*config); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run(config string) error {
	a, err := app.Load(config)
	if err != nil {
		return err
	}
	defer a.Close()
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return a.MCP().Run(ctx, &mcp.StdioTransport{})
}
