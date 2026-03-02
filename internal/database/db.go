// SPDX-License-Identifier: BSD-2-Clause

package database

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"

	_ "modernc.org/sqlite" // SQLite driver
)

const (
	maxOpenConns = 100
	maxIdleConns = 10
)

// DB implements Repository backed by a SQLite file.
type DB struct {
	mu   sync.RWMutex
	conn *sql.DB
}

// New returns a new, unconnected DB.
func New() *DB {
	return &DB{}
}

// ExecRaw executes a raw SQL string directly against the open connection,
// bypassing the read/write enforcement. Intended for test setup only.
func (d *DB) ExecRaw(sql string) (ExecuteResult, error) {
	c, err := d.snapshot()
	if err != nil {
		return ExecuteResult{}, err
	}
	res, err := c.Exec(sql)
	if err != nil {
		return ExecuteResult{}, err
	}
	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()
	return ExecuteResult{RowsAffected: affected, LastInsertID: lastID}, nil
}

// Open opens the SQLite file at path, configures the connection pool, and
// replaces any existing connection. The write lock is held only while swapping
// the handle; I/O is performed outside the lock.
func (d *DB) Open(path string) error {
	newConn, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open %q: %w", path, err)
	}
	newConn.SetMaxOpenConns(maxOpenConns)
	newConn.SetMaxIdleConns(maxIdleConns)

	if err := newConn.Ping(); err != nil {
		newConn.Close()
		return fmt.Errorf("ping %q: %w", path, err)
	}

	d.mu.Lock()
	old := d.conn
	d.conn = newConn
	d.mu.Unlock()

	if old != nil {
		old.Close()
	}
	return nil
}

// Close closes the current connection. The write lock is held only while
// nilling the handle.
func (d *DB) Close() error {
	d.mu.Lock()
	c := d.conn
	d.conn = nil
	d.mu.Unlock()

	if c != nil {
		return c.Close()
	}
	return nil
}

// snapshot safely returns the current connection handle under a read lock.
func (d *DB) snapshot() (*sql.DB, error) {
	d.mu.RLock()
	c := d.conn
	d.mu.RUnlock()
	if c == nil {
		return nil, ErrNoDatabase
	}
	return c, nil
}

// GetSchema returns schema metadata for all user tables and views. If mask is
// non-empty it is applied as a SQL LIKE filter on table/view names.
func (d *DB) GetSchema(mask string) ([]Table, error) {
	c, err := d.snapshot()
	if err != nil {
		return nil, err
	}

	tables, err := enumObjects(c, "table", mask)
	if err != nil {
		return nil, err
	}
	views, err := enumObjects(c, "view", mask)
	if err != nil {
		return nil, err
	}

	result := make([]Table, 0, len(tables)+len(views))

	for _, name := range tables {
		t, err := buildTable(c, name)
		if err != nil {
			return nil, err
		}
		result = append(result, t)
	}
	for _, name := range views {
		v, err := buildView(c, name)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, nil
}

// enumObjects queries sqlite_master for objects of the given type, optionally
// filtered by a LIKE mask.
func enumObjects(c *sql.DB, objType, mask string) ([]string, error) {
	query := `SELECT name FROM sqlite_master WHERE type = ? AND name NOT LIKE 'sqlite_%'`
	args := []any{objType}
	if mask != "" {
		query += ` AND name LIKE ?`
		args = append(args, mask)
	}
	query += ` ORDER BY name`

	rows, err := c.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("enumerate %ss: %w", objType, err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// buildTable constructs a Table (type="table") with columns, indexes, and FKs.
func buildTable(c *sql.DB, name string) (Table, error) {
	t := Table{Name: name, Type: "table"}

	cols, err := tableColumns(c, name)
	if err != nil {
		return t, err
	}
	t.Columns = cols

	idxs, err := tableIndexes(c, name)
	if err != nil {
		return t, err
	}
	t.Indexes = idxs

	fks, err := tableForeignKeys(c, name)
	if err != nil {
		return t, err
	}
	t.ForeignKeys = fks

	return t, nil
}

// buildView constructs a Table (type="view") with columns only.
func buildView(c *sql.DB, name string) (Table, error) {
	t := Table{Name: name, Type: "view"}
	cols, err := tableColumns(c, name)
	if err != nil {
		return t, err
	}
	t.Columns = cols
	return t, nil
}

// tableColumns uses PRAGMA table_info to retrieve column metadata.
func tableColumns(c *sql.DB, table string) ([]Column, error) {
	// PRAGMA table_info returns: cid, name, type, notnull, dflt_value, pk
	rows, err := c.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, fmt.Errorf("table_info %q: %w", table, err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var (
			cid     int
			name    string
			colType string
			notNull int
			dfltVal sql.NullString
			pk      int
		)
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltVal, &pk); err != nil {
			return nil, err
		}
		col := Column{
			Name:       name,
			Type:       colType,
			NotNull:    notNull != 0,
			PrimaryKey: pk != 0,
		}
		if dfltVal.Valid {
			v := dfltVal.String
			col.DefaultValue = &v
		}
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// tableIndexes uses PRAGMA index_list to retrieve user-defined index names.
func tableIndexes(c *sql.DB, table string) ([]string, error) {
	rows, err := c.Query(fmt.Sprintf("PRAGMA index_list(%q)", table))
	if err != nil {
		return nil, fmt.Errorf("index_list %q: %w", table, err)
	}
	defer rows.Close()

	// index_list columns: seq, name, unique, origin, partial
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	nameIdx := 1 // "name" is always the second column
	_ = cols

	var indexes []string
	for rows.Next() {
		dest := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		name := fmt.Sprintf("%s", dest[nameIdx])
		if !strings.HasPrefix(name, "sqlite_autoindex_") {
			indexes = append(indexes, name)
		}
	}
	return indexes, rows.Err()
}

// tableForeignKeys uses PRAGMA foreign_key_list to retrieve FK metadata.
func tableForeignKeys(c *sql.DB, table string) ([]ForeignKey, error) {
	rows, err := c.Query(fmt.Sprintf("PRAGMA foreign_key_list(%q)", table))
	if err != nil {
		return nil, fmt.Errorf("foreign_key_list %q: %w", table, err)
	}
	defer rows.Close()

	var fks []ForeignKey
	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(
			&fk.ID, &fk.Seq, &fk.Table, &fk.From, &fk.To,
			&fk.OnUpdate, &fk.OnDelete, &fk.Match,
		); err != nil {
			return nil, err
		}
		fks = append(fks, fk)
	}
	return fks, rows.Err()
}

// Query executes a read-only SQL statement and returns all rows. It enforces
// that the statement begins with SELECT, WITH, or EXPLAIN. params are optional
// bind arguments substituted for ? placeholders in the SQL statement.
func (d *DB) Query(sqlStr string, params []any) (QueryResult, error) {
	if err := enforceReadOnly(sqlStr); err != nil {
		return QueryResult{}, err
	}

	c, err := d.snapshot()
	if err != nil {
		return QueryResult{}, err
	}

	rows, err := c.Query(sqlStr, params...)
	if err != nil {
		return QueryResult{}, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return QueryResult{}, err
	}

	var resultRows []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return QueryResult{}, err
		}
		row := make(map[string]any, len(cols))
		for i, col := range cols {
			row[col] = vals[i]
		}
		resultRows = append(resultRows, row)
	}
	if err := rows.Err(); err != nil {
		return QueryResult{}, err
	}

	return QueryResult{
		Columns: cols,
		Rows:    resultRows,
		Count:   len(resultRows),
	}, nil
}

// Execute runs a write SQL statement and returns rows affected and last insert
// ID. It rejects SELECT/WITH/EXPLAIN statements. params are optional bind
// arguments substituted for ? placeholders in the SQL statement.
func (d *DB) Execute(sqlStr string, params []any) (ExecuteResult, error) {
	if err := enforceWrite(sqlStr); err != nil {
		return ExecuteResult{}, err
	}

	c, err := d.snapshot()
	if err != nil {
		return ExecuteResult{}, err
	}

	res, err := c.Exec(sqlStr, params...)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("execute: %w", err)
	}

	affected, _ := res.RowsAffected()
	lastID, _ := res.LastInsertId()

	return ExecuteResult{
		RowsAffected: affected,
		LastInsertID: lastID,
	}, nil
}

var readOnlyPrefixes = []string{"select", "with", "explain"}

func statementPrefix(sqlStr string) (string, error) {
	fields := strings.Fields(strings.TrimSpace(sqlStr))
	if len(fields) == 0 {
		return "", fmt.Errorf("SQL statement must not be empty")
	}
	return strings.ToLower(fields[0]), nil
}

func enforceReadOnly(sqlStr string) error {
	prefix, err := statementPrefix(sqlStr)
	if err != nil {
		return err
	}
	for _, p := range readOnlyPrefixes {
		if prefix == p {
			return nil
		}
	}
	return fmt.Errorf("statement type %q is not allowed by query; use execute for write operations", prefix)
}

func enforceWrite(sqlStr string) error {
	prefix, err := statementPrefix(sqlStr)
	if err != nil {
		return err
	}
	for _, p := range readOnlyPrefixes {
		if prefix == p {
			return fmt.Errorf("statement type %q is not allowed by execute; use query for read operations", prefix)
		}
	}
	return nil
}
