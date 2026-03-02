// SPDX-License-Identifier: BSD-2-Clause

package database

import (
	"context"
	"errors"
)

// ErrNoDatabase is returned when a data operation is attempted but no database
// connection is currently open.
var ErrNoDatabase = errors.New("no database is open")

// Repository is the interface that the database layer must satisfy.
type Repository interface {
	Open(path string) error
	GetSchema(mask string) ([]Table, error)
	Query(ctx context.Context, sql string, params []any) (QueryResult, error)
	Execute(ctx context.Context, sql string, params []any) (ExecuteResult, error)
	Close() error
}
