// SPDX-License-Identifier: BSD-2-Clause

package mcp

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/rusq/mcp-sqlite/internal/database"
)

// TestStdioShutdown verifies that StdioServer.Listen returns promptly when its
// context is cancelled, confirming signal-driven graceful shutdown works.
func TestStdioShutdown(t *testing.T) {
	srv := mcpserver.NewMCPServer("shutdown-test", "0.0.0")
	repo := database.New()
	h := New(repo, slog.Default(), 10)
	h.Register(srv)

	stdioSrv := mcpserver.NewStdioServer(srv)

	// Pipe provides a well-formed stdin; writes to pw are never sent so the
	// server blocks waiting for input — exactly as it would in production.
	pr, pw := io.Pipe()
	defer pw.Close()
	defer pr.Close()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- stdioSrv.Listen(ctx, pr, io.Discard)
	}()

	// Give the server a moment to start, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case <-done:
		// Listen returned — shutdown worked.
	case <-time.After(2 * time.Second):
		t.Fatal("StdioServer.Listen did not return within 2 s after context cancellation")
	}
}
