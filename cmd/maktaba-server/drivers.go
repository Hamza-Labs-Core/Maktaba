package main

// Blank-import the SQLite driver so the unified binary can open
// sqlite:// DSNs via database/sql (driver name "sqlite") — e.g. the
// setup wizard pre-creating the database, or future in-process probes.
// modernc.org/sqlite is a pure-Go (CGO-free) implementation, which keeps
// `make server` reproducible and cross-compilable with CGO_ENABLED=0,
// matching the existing api/streaming build flags.
//
// The Postgres path is owned by the api binary (lib/pq); the unified
// binary only needs the SQLite driver registered for its own DSN
// handling. See internal/dsn for scheme classification.
import (
	_ "modernc.org/sqlite"
)
