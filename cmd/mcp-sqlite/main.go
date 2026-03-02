// SPDX-License-Identifier: BSD-2-Clause

package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/rusq/mcp-sqlite/internal/database"
	mcphandler "github.com/rusq/mcp-sqlite/internal/mcp"
)

const serverName = "sqlite-mcp-server"

func main() {
	// Step 1: Parse CLI flags. On failure, flag.Parse calls log.Fatal internally.
	var (
		verbose = flag.Bool("v", false, "enable verbose/debug logging")
		listen  = flag.String("listen", "127.0.0.1:8483", "address to listen on in HTTP mode")
		http    = flag.Bool("http", false, "start in HTTP mode instead of STDIO")
		maxRows = flag.Int("max-rows", 10, "maximum rows returned by the query tool")
	)
	flag.Parse()
	dbPath := flag.Arg(0)

	if *maxRows < 1 {
		log.Fatalf("-max-rows must be at least 1, got %d", *maxRows)
	}

	// Step 2: Initialise logger (stderr only).
	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// Step 3: Validate database file if provided.
	if dbPath != "" {
		if _, err := os.Stat(dbPath); err != nil {
			log.Fatalf("database file not found: %s", dbPath)
		}
	}

	// Step 4: Instantiate the database layer (no connection yet).
	repo := database.New()

	// Step 5: Open the database if a path was given.
	if dbPath != "" {
		if err := repo.Open(dbPath); err != nil {
			log.Fatalf("failed to open database %q: %v", dbPath, err)
		}
		logger.Info("database opened", "path", dbPath)
	}

	// Step 6 & 7: Instantiate handler and register tools with the MCP server.
	mcpSrv := mcpserver.NewMCPServer(serverName, "1.0.0")
	handler := mcphandler.New(repo, logger, *maxRows)
	handler.Register(mcpSrv)

	// Step 8: Register OS signal handlers for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Step 9: Start the event loop.
	if *http {
		logger.Info("starting HTTP server", "addr", *listen)
		httpSrv := mcpserver.NewStreamableHTTPServer(mcpSrv)
		go func() {
			<-ctx.Done()
			logger.Info("shutting down HTTP server")
			httpSrv.Shutdown(context.Background()) //nolint:errcheck
		}()
		if err := httpSrv.Start(*listen); err != nil {
			log.Fatalf("HTTP server error: %v", err)
		}
	} else {
		logger.Info("starting STDIO server")
		// Use StdioServer.Listen directly so our signal context drives shutdown.
		stdioSrv := mcpserver.NewStdioServer(mcpSrv)
		if err := stdioSrv.Listen(ctx, os.Stdin, os.Stdout); err != nil {
			log.Fatalf("STDIO server error: %v", err)
		}
	}

	if err := repo.Close(); err != nil {
		logger.Warn("error closing database on shutdown", "err", err)
	}
}
