// SPDX-License-Identifier: BSD-2-Clause

package database

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// init suppresses log output during tests.
func init() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.Level(99), // above any real level — silences all output
	})))
}

// newTestDB opens a fresh temporary SQLite database seeded with a known schema.
func newTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")

	db := New()
	if err := db.Open(path); err != nil {
		t.Fatalf("open: %v", err)
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
			total   REAL    NOT NULL DEFAULT 0.0
		);
		CREATE INDEX idx_orders_user ON orders(user_id);
		CREATE VIEW active_users AS SELECT id, name FROM users WHERE age >= 18;
		INSERT INTO users (name, age) VALUES ('Alice', 30), ('Bob', 17), ('Carol', 25);
		INSERT INTO orders (user_id, total) VALUES (1, 99.9), (3, 49.5);
	`
	if _, err := db.conn.Exec(seed); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db, path
}

// ── GetSchema ────────────────────────────────────────────────────────────────

func TestGetSchema_TablesAndColumns(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	tables, err := db.GetSchema("")
	if err != nil {
		t.Fatalf("GetSchema: %v", err)
	}

	byName := map[string]Table{}
	for _, tbl := range tables {
		byName[tbl.Name] = tbl
	}

	// users table
	users, ok := byName["users"]
	if !ok {
		t.Fatal("expected table 'users'")
	}
	if users.Type != "table" {
		t.Errorf("users.Type = %q, want %q", users.Type, "table")
	}
	if len(users.Columns) != 3 {
		t.Errorf("users: want 3 columns, got %d", len(users.Columns))
	}

	// orders table — has FK and index
	orders, ok := byName["orders"]
	if !ok {
		t.Fatal("expected table 'orders'")
	}
	if len(orders.Indexes) != 1 || orders.Indexes[0] != "idx_orders_user" {
		t.Errorf("orders.Indexes = %v, want [idx_orders_user]", orders.Indexes)
	}
	if len(orders.ForeignKeys) != 1 {
		t.Errorf("orders: want 1 FK, got %d", len(orders.ForeignKeys))
	} else {
		fk := orders.ForeignKeys[0]
		if fk.Table != "users" || fk.From != "user_id" || fk.To != "id" {
			t.Errorf("orders FK = %+v, unexpected values", fk)
		}
	}

	// view
	view, ok := byName["active_users"]
	if !ok {
		t.Fatal("expected view 'active_users'")
	}
	if view.Type != "view" {
		t.Errorf("active_users.Type = %q, want %q", view.Type, "view")
	}
	if len(view.Indexes) != 0 || len(view.ForeignKeys) != 0 {
		t.Error("view should have no indexes or foreign keys")
	}
}

func TestGetSchema_Mask(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	tables, err := db.GetSchema("user%")
	if err != nil {
		t.Fatalf("GetSchema with mask: %v", err)
	}
	for _, tbl := range tables {
		if tbl.Name != "users" {
			t.Errorf("mask filter returned unexpected table %q", tbl.Name)
		}
	}
}

func TestGetSchema_NoDatabase(t *testing.T) {
	db := New()
	_, err := db.GetSchema("")
	if err == nil {
		t.Fatal("expected error when no database is open")
	}
}

// ── Query ─────────────────────────────────────────────────────────────────────

func TestQuery_Select(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	res, err := db.Query("SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if res.Count != 3 {
		t.Errorf("want 3 rows, got %d", res.Count)
	}
	if res.Rows[0]["name"] != "Alice" {
		t.Errorf("first row name = %v, want Alice", res.Rows[0]["name"])
	}
}

func TestQuery_WithCTE(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	_, err := db.Query("WITH cte AS (SELECT 1 AS n) SELECT n FROM cte")
	if err != nil {
		t.Fatalf("WITH query: %v", err)
	}
}

func TestQuery_Explain(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	_, err := db.Query("EXPLAIN SELECT * FROM users")
	if err != nil {
		t.Fatalf("EXPLAIN query: %v", err)
	}
}

func TestQuery_RejectsNonSelect(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	_, err := db.Query("INSERT INTO users(name,age) VALUES('X',1)")
	if err == nil {
		t.Fatal("expected error for non-SELECT statement")
	}
}

// ── Execute ───────────────────────────────────────────────────────────────────

func TestExecute_Insert(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	res, err := db.Execute("INSERT INTO users(name, age) VALUES('Dave', 40)")
	if err != nil {
		t.Fatalf("Execute INSERT: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
	if res.LastInsertID != 4 {
		t.Errorf("LastInsertID = %d, want 4", res.LastInsertID)
	}
}

func TestExecute_Update(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	res, err := db.Execute("UPDATE users SET age = 31 WHERE name = 'Alice'")
	if err != nil {
		t.Fatalf("Execute UPDATE: %v", err)
	}
	if res.RowsAffected != 1 {
		t.Errorf("RowsAffected = %d, want 1", res.RowsAffected)
	}
}

func TestExecute_RejectsSelect(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	_, err := db.Execute("SELECT * FROM users")
	if err == nil {
		t.Fatal("expected error for SELECT in Execute")
	}
}

func TestQuery_RejectsEmpty(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	for _, sql := range []string{"", "   ", "\t\n"} {
		_, err := db.Query(sql)
		if err == nil {
			t.Errorf("Query(%q): expected error for empty SQL, got nil", sql)
		}
	}
}

func TestExecute_RejectsEmpty(t *testing.T) {
	db, _ := newTestDB(t)
	defer db.Close()

	for _, sql := range []string{"", "   ", "\t\n"} {
		_, err := db.Execute(sql)
		if err == nil {
			t.Errorf("Execute(%q): expected error for empty SQL, got nil", sql)
		}
	}
}
