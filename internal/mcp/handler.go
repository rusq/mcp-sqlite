// SPDX-License-Identifier: BSD-2-Clause

package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"github.com/rusq/mcp-sqlite/internal/database"
)

const sqlLogMaxLen = 100

// Handler holds the MCP tool handlers and the concurrency gate.
type Handler struct {
	repo         database.Repository
	logger       *slog.Logger
	maxRows      int
	queryTimeout time.Duration

	// gate is a second RWMutex at the handler layer.
	// open_database holds the write lock; data ops hold the read lock.
	gate sync.RWMutex

	// openInProgress is a CAS flag (0=idle, 1=busy) that prevents a second
	// concurrent open_database from queuing on the write lock.
	openInProgress atomic.Int32
}

// New returns a Handler ready to register tools. queryTimeout is applied as a
// deadline to every query and execute call.
func New(repo database.Repository, logger *slog.Logger, maxRows int, queryTimeout time.Duration) *Handler {
	return &Handler{repo: repo, logger: logger, maxRows: maxRows, queryTimeout: queryTimeout}
}

// Register registers all four tools with the given MCP server.
func (h *Handler) Register(s *server.MCPServer) {
	s.AddTool(
		mcpgo.NewTool("open_database",
			mcpgo.WithDescription("Open a SQLite database file. Must be called before any other tool if no database was specified at startup."),
			mcpgo.WithString("path",
				mcpgo.Required(),
				mcpgo.Description("Absolute or relative path to an existing .db or .sqlite file"),
			),
		),
		h.handleOpenDatabase,
	)

	s.AddTool(
		mcpgo.NewTool("get_schema",
			mcpgo.WithDescription("List tables and views in the currently open SQLite database, including columns, indexes, and foreign keys."),
			mcpgo.WithString("mask",
				mcpgo.Description("SQL LIKE mask to limit output, e.g. 'USER_%'"),
			),
		),
		h.handleGetSchema,
	)

	s.AddTool(
		mcpgo.NewTool("query",
			mcpgo.WithDescription("Execute a read-only SQL query (SELECT, WITH, EXPLAIN)."),
			mcpgo.WithString("sql",
				mcpgo.Required(),
				mcpgo.Description("A read-only SQL statement, may contain ? placeholders"),
			),
			mcpgo.WithArray("params",
				mcpgo.Description("Optional bind parameters for ? placeholders, e.g. [\"John\", 42]"),
			),
		),
		h.handleQuery,
	)

	s.AddTool(
		mcpgo.NewTool("execute",
			mcpgo.WithDescription("Execute a write SQL operation (INSERT, UPDATE, DELETE, DDL, etc.)."),
			mcpgo.WithString("sql",
				mcpgo.Required(),
				mcpgo.Description("A SQL statement that modifies the database, may contain ? placeholders"),
			),
			mcpgo.WithArray("params",
				mcpgo.Description("Optional bind parameters for ? placeholders, e.g. [\"John\", 42]"),
			),
		),
		h.handleExecute,
	)
}

// errResult encodes an error as an MCP error result (no language-level error returned).
func errResult(msg string) (*mcpgo.CallToolResult, error) {
	return mcpgo.NewToolResultError(msg), nil
}

// truncateSQL truncates a SQL string for safe logging.
func truncateSQL(s string) string {
	if len(s) > sqlLogMaxLen {
		return s[:sqlLogMaxLen] + "…"
	}
	return s
}

// handleOpenDatabase implements the open_database tool.
func (h *Handler) handleOpenDatabase(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	path := strings.TrimSpace(req.GetString("path", ""))
	if path == "" {
		h.logger.Warn("open_database: missing required parameter 'path'")
		return errResult("parameter 'path' is required and must be a non-empty string")
	}

	// CAS guard: only one open_database may be in progress at a time.
	if !h.openInProgress.CompareAndSwap(0, 1) {
		h.logger.Warn("open_database: rejected concurrent call", "path", path)
		return errResult("an open_database operation is already in progress")
	}
	defer h.openInProgress.Store(0)

	// Acquire the write lock — blocks until all in-flight data ops finish.
	h.gate.Lock()
	defer h.gate.Unlock()

	// Verify the file exists before attempting to open.
	if _, err := os.Stat(path); err != nil {
		h.logger.Warn("open_database: file not found", "path", path)
		return errResult(fmt.Sprintf("database file not found: %s", path))
	}

	if err := h.repo.Open(path); err != nil {
		h.logger.Error("open_database: open failed", "path", path, "err", err)
		return errResult(fmt.Sprintf("failed to open database: %v", err))
	}

	tables, err := h.repo.GetSchema("")
	if err != nil {
		h.logger.Error("open_database: get_schema after open failed", "err", err)
		return errResult(fmt.Sprintf("database opened but schema retrieval failed: %v", err))
	}

	h.logger.Info("open_database: success", "path", path)
	return mcpgo.NewToolResultText(formatSchema(tables)), nil
}

// handleGetSchema implements the get_schema tool.
func (h *Handler) handleGetSchema(_ context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	mask := req.GetString("mask", "")

	h.gate.RLock()
	defer h.gate.RUnlock()

	tables, err := h.repo.GetSchema(mask)
	if err != nil {
		if errors.Is(err, database.ErrNoDatabase) {
			h.logger.Warn("get_schema: no database open")
			return errResult("No database is open. Call open_database first.")
		}
		h.logger.Error("get_schema failed", "err", err)
		return errResult(fmt.Sprintf("get_schema failed: %v", err))
	}

	return mcpgo.NewToolResultText(formatSchema(tables)), nil
}

// getParams extracts the optional "params" array from a tool request,
// preserving the native JSON types (string, float64, bool, nil) of each item.
// Returns (nil, nil) if the key is absent or null.
// Returns a non-nil error if "params" is present but not a JSON array.
func getParams(req mcpgo.CallToolRequest) ([]any, error) {
	v, ok := req.GetArguments()["params"]
	if !ok || v == nil {
		return nil, nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("parameter 'params' must be an array, got %T", v)
	}
	return arr, nil
}

// handleQuery implements the query tool.
func (h *Handler) handleQuery(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	sqlStr := strings.TrimSpace(req.GetString("sql", ""))
	if sqlStr == "" {
		h.logger.Warn("query: missing required parameter 'sql'")
		return errResult("parameter 'sql' is required and must be a non-empty string")
	}
	params, err := getParams(req)
	if err != nil {
		h.logger.Warn("query: invalid params", "err", err)
		return errResult(err.Error())
	}

	h.gate.RLock()
	defer h.gate.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, h.queryTimeout)
	defer cancel()

	result, err := h.repo.Query(ctx, sqlStr, params)
	if err != nil {
		if errors.Is(err, database.ErrNoDatabase) {
			h.logger.Warn("query: no database open")
			return errResult("No database is open. Call open_database first.")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			h.logger.Warn("query: timed out", "sql", truncateSQL(sqlStr), "timeout", h.queryTimeout)
			return errResult(fmt.Sprintf("query timed out after %v", h.queryTimeout))
		}
		h.logger.Error("query failed", "sql", truncateSQL(sqlStr), "err", err)
		return errResult(fmt.Sprintf("query failed: %v", err))
	}

	return mcpgo.NewToolResultText(formatQuery(result, h.maxRows)), nil
}

// handleExecute implements the execute tool.
func (h *Handler) handleExecute(ctx context.Context, req mcpgo.CallToolRequest) (*mcpgo.CallToolResult, error) {
	sqlStr := strings.TrimSpace(req.GetString("sql", ""))
	if sqlStr == "" {
		h.logger.Warn("execute: missing required parameter 'sql'")
		return errResult("parameter 'sql' is required and must be a non-empty string")
	}
	params, err := getParams(req)
	if err != nil {
		h.logger.Warn("execute: invalid params", "err", err)
		return errResult(err.Error())
	}

	h.gate.RLock()
	defer h.gate.RUnlock()

	ctx, cancel := context.WithTimeout(ctx, h.queryTimeout)
	defer cancel()

	result, err := h.repo.Execute(ctx, sqlStr, params)
	if err != nil {
		if errors.Is(err, database.ErrNoDatabase) {
			h.logger.Warn("execute: no database open")
			return errResult("No database is open. Call open_database first.")
		}
		if errors.Is(err, context.DeadlineExceeded) {
			h.logger.Warn("execute: timed out", "sql", truncateSQL(sqlStr), "timeout", h.queryTimeout)
			return errResult(fmt.Sprintf("execute timed out after %v", h.queryTimeout))
		}
		h.logger.Error("execute failed", "sql", truncateSQL(sqlStr), "err", err)
		return errResult(fmt.Sprintf("execute failed: %v", err))
	}

	return mcpgo.NewToolResultText(formatExecute(result)), nil
}
