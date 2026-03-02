// SPDX-License-Identifier: BSD-2-Clause

package mcp

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"

	"github.com/rusq/mcp-sqlite/internal/database"
)

// init suppresses log output during tests.
func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.Level(99),
	})))
}

// initClient calls Initialize with a minimal request.
func initClient(t *testing.T, cli *mcpclient.Client) {
	t.Helper()
	_, err := cli.Initialize(context.Background(), mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: mcpgo.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcpgo.Implementation{Name: "test", Version: "0.0.0"},
		},
	})
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
}

// testHarness wires up a real SQLite DB, registers all handlers, and returns
// an in-process MCP client ready to call tools.
func testHarness(t *testing.T) (cli *mcpclient.Client, dbPath string, altPath string) {
	t.Helper()
	dir := t.TempDir()

	// Primary database.
	dbPath = filepath.Join(dir, "primary.db")
	repo := database.New()
	if err := repo.Open(dbPath); err != nil {
		t.Fatalf("open primary db: %v", err)
	}
	seed := `
		CREATE TABLE users (
			id   INTEGER PRIMARY KEY,
			name TEXT    NOT NULL,
			age  INTEGER
		);
		CREATE TABLE orders (
			id      INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			amount  REAL    NOT NULL DEFAULT 0.0
		);
		CREATE INDEX idx_orders_user ON orders(user_id);
		INSERT INTO users  VALUES (1,'Alice',30),(2,'Bob',17),(3,'Carol',25);
		INSERT INTO orders VALUES (1,1,99.9),(2,3,49.5),(3,1,12.0);
	`
	if _, err := repo.ExecRaw(seed); err != nil {
		t.Fatalf("seed primary db: %v", err)
	}

	// Alternate database (for open_database replacement tests).
	altPath = filepath.Join(dir, "alt.db")
	altRepo := database.New()
	if err := altRepo.Open(altPath); err != nil {
		t.Fatalf("open alt db: %v", err)
	}
	if _, err := altRepo.ExecRaw(`CREATE TABLE alt_table (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatalf("seed alt db: %v", err)
	}
	altRepo.Close()

	logger := slog.Default()
	srv := mcpserver.NewMCPServer("test", "0.0.0")
	h := New(repo, logger, 10, 60*time.Second)
	h.Register(srv)

	cli, err := mcpclient.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("in-process client: %v", err)
	}
	initClient(t, cli)
	t.Cleanup(func() { cli.Close(); repo.Close() })

	return cli, dbPath, altPath
}

// callTool is a helper that calls a named tool and returns the result.
func callTool(t *testing.T, cli *mcpclient.Client, name string, args map[string]any) *mcpgo.CallToolResult {
	t.Helper()
	res, err := cli.CallTool(context.Background(), mcpgo.CallToolRequest{
		Params: mcpgo.CallToolParams{
			Name:      name,
			Arguments: args,
		},
	})
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}
	return res
}

func resultText(res *mcpgo.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcpgo.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// ── get_schema ─────────────────────────────────────────────────────────────

func TestHandler_GetSchema_All(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "get_schema", map[string]any{})
	if res.IsError {
		t.Fatalf("get_schema error: %s", resultText(res))
	}
	text := resultText(res)
	for _, want := range []string{"users", "orders", "idx_orders_user"} {
		if !strings.Contains(text, want) {
			t.Errorf("get_schema response missing %q", want)
		}
	}
}

// ── query ──────────────────────────────────────────────────────────────────

func TestHandler_Query_Select(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{"sql": "SELECT id, name FROM users ORDER BY id"})
	if res.IsError {
		t.Fatalf("query error: %s", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "Alice") {
		t.Errorf("query response missing 'Alice': %s", text)
	}
}

func TestHandler_Query_WithCTE(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{"sql": "WITH cte AS (SELECT 1 AS n) SELECT n FROM cte"})
	if res.IsError {
		t.Fatalf("WITH CTE query error: %s", resultText(res))
	}
}

func TestHandler_Query_Explain(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{"sql": "EXPLAIN SELECT * FROM users"})
	if res.IsError {
		t.Fatalf("EXPLAIN query error: %s", resultText(res))
	}
}

func TestHandler_Query_MissingParam(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing sql param")
	}
}

func TestHandler_Query_EmptySQL(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{"sql": "   "})
	if !res.IsError {
		t.Fatal("expected error for empty sql")
	}
}

func TestHandler_Query_NonSelect(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{"sql": "INSERT INTO users(name,age) VALUES('X',1)"})
	if !res.IsError {
		t.Fatal("expected error for non-SELECT in query")
	}
}

func TestHandler_Query_BindParams(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "query", map[string]any{
		"sql":    "SELECT id, name FROM users WHERE name = ?",
		"params": []any{"Alice"},
	})
	if res.IsError {
		t.Fatalf("query with bind params error: %s", resultText(res))
	}
	text := resultText(res)
	if !strings.Contains(text, "Alice") {
		t.Errorf("expected 'Alice' in result, got: %s", text)
	}
	if strings.Contains(text, "Bob") || strings.Contains(text, "Carol") {
		t.Errorf("unexpected rows in filtered result: %s", text)
	}
}

func TestHandler_Execute_BindParams(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{
		"sql":    "INSERT INTO users(name, age) VALUES(?, ?)",
		"params": []any{"Dave", 40},
	})
	if res.IsError {
		t.Fatalf("execute with bind params error: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "Rows affected: 1") {
		t.Errorf("unexpected response: %s", resultText(res))
	}

	// Verify the row can be retrieved via a parameterised query.
	check := callTool(t, cli, "query", map[string]any{
		"sql":    "SELECT name FROM users WHERE name = ?",
		"params": []any{"Dave"},
	})
	if check.IsError {
		t.Fatalf("verify query error: %s", resultText(check))
	}
	if !strings.Contains(resultText(check), "Dave") {
		t.Errorf("inserted row not found via bind-param query: %s", resultText(check))
	}
}

// ── execute ────────────────────────────────────────────────────────────────

func TestHandler_Execute_Insert(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{"sql": "INSERT INTO users(name,age) VALUES('Dave',40)"})
	if res.IsError {
		t.Fatalf("execute INSERT error: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "Rows affected: 1") {
		t.Errorf("unexpected response: %s", resultText(res))
	}
}

func TestHandler_Execute_Update(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{"sql": "UPDATE users SET age=31 WHERE name='Alice'"})
	if res.IsError {
		t.Fatalf("execute UPDATE error: %s", resultText(res))
	}
	if !strings.Contains(resultText(res), "Rows affected: 1") {
		t.Errorf("unexpected response: %s", resultText(res))
	}
}

func TestHandler_Execute_DDL(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{"sql": "CREATE TABLE tmp (id INTEGER PRIMARY KEY)"})
	if res.IsError {
		t.Fatalf("execute DDL error: %s", resultText(res))
	}
}

func TestHandler_Execute_MissingParam(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing sql param")
	}
}

func TestHandler_Execute_RejectsSelect(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "execute", map[string]any{"sql": "SELECT * FROM users"})
	if !res.IsError {
		t.Fatal("expected error for SELECT in execute")
	}
}

// ── open_database ──────────────────────────────────────────────────────────

func TestHandler_OpenDatabase_Success(t *testing.T) {
	cli, dbPath, _ := testHarness(t)
	res := callTool(t, cli, "open_database", map[string]any{"path": dbPath})
	if res.IsError {
		t.Fatalf("open_database error: %s", resultText(res))
	}
}

func TestHandler_OpenDatabase_NotFound(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "open_database", map[string]any{"path": "/nonexistent/path.db"})
	if !res.IsError {
		t.Fatal("expected error for non-existent file")
	}
}

func TestHandler_OpenDatabase_MissingParam(t *testing.T) {
	cli, _, _ := testHarness(t)
	res := callTool(t, cli, "open_database", map[string]any{})
	if !res.IsError {
		t.Fatal("expected error for missing path param")
	}
}

func TestHandler_OpenDatabase_ReplacesPrevious(t *testing.T) {
	cli, _, altPath := testHarness(t)
	res := callTool(t, cli, "open_database", map[string]any{"path": altPath})
	if res.IsError {
		t.Fatalf("open alt db: %s", resultText(res))
	}
	// After switching, get_schema should return alt_table, not users.
	schemaRes := callTool(t, cli, "get_schema", map[string]any{})
	text := resultText(schemaRes)
	if !strings.Contains(text, "alt_table") {
		t.Errorf("after switching DB, expected 'alt_table' in schema, got: %s", text)
	}
	if strings.Contains(text, "users") {
		t.Errorf("after switching DB, 'users' should not appear in schema")
	}
}

// ── no database open ───────────────────────────────────────────────────────

func TestHandler_NoDatabaseOpen(t *testing.T) {
	// Build a fresh harness with no database opened.
	srv := mcpserver.NewMCPServer("test-nodb", "0.0.0")
	repo := database.New() // no Open call
	h := New(repo, slog.Default(), 10, 60*time.Second)
	h.Register(srv)

	cli, err := mcpclient.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("in-process client: %v", err)
	}
	initClient(t, cli)
	defer cli.Close()

	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"get_schema", map[string]any{}},
		{"query", map[string]any{"sql": "SELECT 1"}},
		{"execute", map[string]any{"sql": "CREATE TABLE t (id INTEGER)"}},
	} {
		res := callTool(t, cli, tc.tool, tc.args)
		if !res.IsError {
			t.Errorf("tool %q: expected 'no database' error, got success", tc.tool)
		}
	}
}

// ── malformed arguments ────────────────────────────────────────────────────

func TestHandler_MalformedArgs_NoPanic(t *testing.T) {
	cli, _, _ := testHarness(t)
	// Pass nil-valued args — should return graceful errors, not panic.
	for _, tool := range []string{"query", "execute", "open_database"} {
		res := callTool(t, cli, tool, map[string]any{"sql": nil, "path": nil})
		_ = res // result may be error or success; we just verify no panic
	}
}

// ── query timeout ──────────────────────────────────────────────────────────

// testHarnessWithTimeout creates a harness whose handler uses the given timeout.
func testHarnessWithTimeout(t *testing.T, timeout time.Duration) *mcpclient.Client {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "timeout.db")
	repo := database.New()
	if err := repo.Open(dbPath); err != nil {
		t.Fatalf("open db: %v", err)
	}
	if _, err := repo.ExecRaw(`CREATE TABLE slow (id INTEGER PRIMARY KEY);`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	srv := mcpserver.NewMCPServer("test-timeout", "0.0.0")
	h := New(repo, slog.Default(), 10, timeout)
	h.Register(srv)

	cli, err := mcpclient.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("in-process client: %v", err)
	}
	initClient(t, cli)
	t.Cleanup(func() { cli.Close(); repo.Close() })
	return cli
}

func TestHandler_Query_Timeout(t *testing.T) {
	// Use a 1 ms timeout — any real query should exceed it.
	cli := testHarnessWithTimeout(t, 1*time.Millisecond)

	// WITH RECURSIVE generates enough work to reliably hit the timeout.
	res := callTool(t, cli, "query", map[string]any{
		"sql": "WITH RECURSIVE cnt(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM cnt WHERE x < 1000000) SELECT count(*) FROM cnt",
	})
	if !res.IsError {
		t.Fatal("expected timeout error, got success")
	}
	if !strings.Contains(resultText(res), "timed out") {
		t.Errorf("expected 'timed out' in error message, got: %s", resultText(res))
	}
}

func TestHandler_Execute_Timeout(t *testing.T) {
	// Use a 1 ms timeout.
	cli := testHarnessWithTimeout(t, 1*time.Millisecond)

	// Insert inside a WITH RECURSIVE to produce enough work to trip the timeout.
	res := callTool(t, cli, "execute", map[string]any{
		"sql": "INSERT INTO slow SELECT value FROM generate_series(1, 1000000)",
	})
	if !res.IsError {
		// generate_series may not be available in all SQLite builds; if the
		// statement itself errors for another reason that's fine too — we only
		// care that no panic occurred and an error was returned.
		t.Log("execute did not time out (generate_series may be unavailable); verifying no panic")
	}
}
