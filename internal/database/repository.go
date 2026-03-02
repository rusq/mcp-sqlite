// SPDX-License-Identifier: BSD-2-Clause

package database

import "errors"

// ErrNoDatabase is returned when a data operation is attempted but no database
// connection is currently open.
var ErrNoDatabase = errors.New("no database is open")

// Repository is the interface that the database layer must satisfy.
type Repository interface {
	Open(path string) error
	GetSchema(mask string) ([]Table, error)
	Query(sql string) (QueryResult, error)
	Execute(sql string) (ExecuteResult, error)
	Close() error
}
