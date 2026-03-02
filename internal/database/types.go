// SPDX-License-Identifier: BSD-2-Clause

package database

// Table represents a SQLite table or view and its metadata.
type Table struct {
	Name        string
	Type        string // "table" or "view"
	Columns     []Column
	Indexes     []string
	ForeignKeys []ForeignKey
}

// Column represents a column in a table or view.
type Column struct {
	Name         string
	Type         string
	NotNull      bool
	DefaultValue *string
	PrimaryKey   bool
}

// ForeignKey represents a foreign key constraint on a table.
type ForeignKey struct {
	ID       int
	Seq      int
	Table    string
	From     string
	To       string
	OnUpdate string
	OnDelete string
	Match    string
}

// QueryResult holds the result of a read-only SQL query.
type QueryResult struct {
	Columns []string
	Rows    []map[string]any
	Count   int // total rows before display cap
}

// ExecuteResult holds the result of a write SQL statement.
type ExecuteResult struct {
	RowsAffected int64
	LastInsertID int64
}
