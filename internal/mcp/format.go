// SPDX-License-Identifier: BSD-2-Clause

package mcp

import (
	"fmt"
	"strings"

	"github.com/rusq/mcp-sqlite/internal/database"
)

// formatSchema renders a slice of Table values as a human-readable plain-text
// string matching the format defined in §7.1 of the spec.
func formatSchema(tables []database.Table) string {
	var sb strings.Builder

	// Separate tables and views.
	var tbls, views []database.Table
	for _, t := range tables {
		if t.Type == "view" {
			views = append(views, t)
		} else {
			tbls = append(tbls, t)
		}
	}

	sb.WriteString("Tables:\n")
	for _, t := range tbls {
		sb.WriteString("\n")
		sb.WriteString(t.Name)
		sb.WriteString("\n")

		sb.WriteString("Columns:\n")
		for _, c := range t.Columns {
			sb.WriteString(formatColumn(c, true))
		}

		if len(t.Indexes) > 0 {
			sb.WriteString("Indexes:\n")
			for _, idx := range t.Indexes {
				fmt.Fprintf(&sb, "  - %s\n", idx)
			}
		}

		if len(t.ForeignKeys) > 0 {
			sb.WriteString("Foreign Keys:\n")
			for _, fk := range t.ForeignKeys {
				sb.WriteString(formatForeignKey(fk))
			}
		}
	}

	sb.WriteString("\nViews:\n")
	for _, v := range views {
		sb.WriteString("\n")
		sb.WriteString(v.Name)
		sb.WriteString("\n")

		sb.WriteString("Columns:\n")
		for _, c := range v.Columns {
			sb.WriteString(formatColumn(c, false))
		}
	}

	sb.WriteString("\n")
	return sb.String()
}

// formatColumn renders a single column line. includePK controls whether the
// PRIMARY KEY annotation is emitted (not applicable for views).
func formatColumn(c database.Column, includePK bool) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "  - %s (%s)", c.Name, c.Type)
	if c.NotNull {
		sb.WriteString(" [NOT NULL]")
	}
	if includePK && c.PrimaryKey {
		sb.WriteString(" [PRIMARY KEY]")
	}
	if c.DefaultValue != nil {
		fmt.Fprintf(&sb, " [DEFAULT: %s]", *c.DefaultValue)
	}
	sb.WriteString("\n")
	return sb.String()
}

// formatForeignKey renders a single foreign key line.
func formatForeignKey(fk database.ForeignKey) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "  - %s -> %s(%s)", fk.From, fk.Table, fk.To)
	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		fmt.Fprintf(&sb, " [ON DELETE %s]", fk.OnDelete)
	}
	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		fmt.Fprintf(&sb, " [ON UPDATE %s]", fk.OnUpdate)
	}
	sb.WriteString("\n")
	return sb.String()
}

// formatQuery renders a QueryResult as a human-readable plain-text string
// matching the format defined in §7.2, capping displayed rows at maxRows.
func formatQuery(r database.QueryResult, maxRows int) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "Columns: %s\n", strings.Join(r.Columns, ", "))
	fmt.Fprintf(&sb, "Number of Rows: %d\n", r.Count)

	display := r.Rows
	if maxRows > 0 && len(display) > maxRows {
		display = display[:maxRows]
	}

	for i, row := range display {
		fmt.Fprintf(&sb, "\nRow %d:\n", i+1)
		for _, col := range r.Columns {
			fmt.Fprintf(&sb, "  %s = %v\n", col, row[col])
		}
	}

	if r.Count > maxRows {
		fmt.Fprintf(&sb, "\n(Showing %d of %d rows)\n", maxRows, r.Count)
	}

	return sb.String()
}

// formatExecute renders an ExecuteResult as a human-readable plain-text string
// matching the format defined in §7.3.
func formatExecute(r database.ExecuteResult) string {
	return fmt.Sprintf("Rows affected: %d\nLast insert ID: %d\n", r.RowsAffected, r.LastInsertID)
}
