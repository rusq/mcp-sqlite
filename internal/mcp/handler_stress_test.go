// SPDX-License-Identifier: BSD-2-Clause

package mcp

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/rusq/mcp-sqlite/internal/database"
)

// TestHandler_ConcurrencyStress exercises the handler under high concurrency
// with the race detector enabled. Run with: go test -race ./mcp/...
func TestHandler_ConcurrencyStress(t *testing.T) {
	dir := t.TempDir()

	// Create two valid databases to alternate between.
	paths := [2]string{
		filepath.Join(dir, "stress_a.db"),
		filepath.Join(dir, "stress_b.db"),
	}
	for _, p := range paths {
		db := database.New()
		if err := db.Open(p); err != nil {
			t.Fatalf("open stress db %s: %v", p, err)
		}
		if _, err := db.ExecRaw(`CREATE TABLE t (id INTEGER PRIMARY KEY, v TEXT); INSERT INTO t VALUES (1,'hello');`); err != nil {
			t.Fatalf("seed stress db %s: %v", p, err)
		}
		db.Close()
	}

	// Start with paths[0] open.
	repo := database.New()
	if err := repo.Open(paths[0]); err != nil {
		t.Fatalf("initial open: %v", err)
	}
	defer repo.Close()

	srv := mcpserver.NewMCPServer("stress", "0.0.0")
	h := New(repo, slog.Default(), 10, 60*time.Second)
	h.Register(srv)

	// newClient is a helper that creates and initialises a fresh in-process client.
	newClient := func(t *testing.T) *mcpclient.Client {
		t.Helper()
		cli, err := mcpclient.NewInProcessClient(srv)
		if err != nil {
			t.Fatalf("new client: %v", err)
		}
		initClient(t, cli)
		return cli
	}

	const (
		dataWorkers = 20
		openWorkers = 5
		iterations  = 30
	)

	var wg sync.WaitGroup

	// Data operation goroutines: query, execute, get_schema in a tight loop.
	for i := 0; i < dataWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cli := newClient(t)
			defer cli.Close()

			for iter := 0; iter < iterations; iter++ {
				// get_schema
				res, err := cli.CallTool(context.Background(), mcpgo.CallToolRequest{
					Params: mcpgo.CallToolParams{Name: "get_schema", Arguments: map[string]any{}},
				})
				if err != nil {
					t.Errorf("worker %d get_schema err: %v", id, err)
					return
				}
				// A handler-level error is acceptable (e.g. open_database swapping
				// underneath), but we must not crash.
				_ = res

				// query
				res, err = cli.CallTool(context.Background(), mcpgo.CallToolRequest{
					Params: mcpgo.CallToolParams{
						Name:      "query",
						Arguments: map[string]any{"sql": "SELECT id, v FROM t"},
					},
				})
				if err != nil {
					t.Errorf("worker %d query err: %v", id, err)
					return
				}
				_ = res

				// execute — insert then delete to keep DB clean-ish
				val := fmt.Sprintf("stress-%d-%d", id, iter)
				res, err = cli.CallTool(context.Background(), mcpgo.CallToolRequest{
					Params: mcpgo.CallToolParams{
						Name:      "execute",
						Arguments: map[string]any{"sql": fmt.Sprintf("INSERT OR IGNORE INTO t VALUES (%d, '%s')", id*1000+iter+10, val)},
					},
				})
				if err != nil {
					t.Errorf("worker %d execute err: %v", id, err)
					return
				}
				_ = res
			}
		}(i)
	}

	// open_database goroutines: alternate between the two databases.
	for i := 0; i < openWorkers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			cli := newClient(t)
			defer cli.Close()

			for iter := 0; iter < iterations; iter++ {
				path := paths[iter%2]
				res, err := cli.CallTool(context.Background(), mcpgo.CallToolRequest{
					Params: mcpgo.CallToolParams{
						Name:      "open_database",
						Arguments: map[string]any{"path": path},
					},
				})
				if err != nil {
					t.Errorf("open worker %d err: %v", id, err)
					return
				}
				// A "concurrent open" error result is acceptable; a crash is not.
				_ = res
			}
		}(i)
	}

	wg.Wait()

	// Final sanity check: the handler is still functional after the storm.
	cli := newClient(t)
	defer cli.Close()
	res, err := cli.CallTool(context.Background(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{Name: "get_schema", Arguments: map[string]any{}},
	})
	if err != nil {
		t.Fatalf("post-storm get_schema err: %v", err)
	}
	if res.IsError {
		t.Errorf("post-storm get_schema returned error: %s", resultText(res))
	}
}
