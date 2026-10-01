// SPDX-License-Identifier: AGPL-3.0-or-later

// Package store opens the SQLite database of Home-Mandate and keeps its schema current.
//
// The database is created with owner-only permissions in a directory that others cannot
// write to. Every connection enforces foreign keys, WAL journaling and synchronous=FULL,
// so that the audit log and approval requests survive a power loss. Migrations are
// embedded and applied by goose; the store refuses to start if an applied migration was
// changed or the database is newer than the binary.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite" // registers the pure-Go "sqlite" driver
)

var (
	// ErrInvalidPath means the database path is not an absolute path to a file.
	ErrInvalidPath = errors.New("store: invalid database path")
	// ErrInsecurePermissions means the database or its directory is accessible to others.
	ErrInsecurePermissions = errors.New("store: insecure permissions")
)

const (
	driverName = "sqlite"
	// busyTimeoutMillis bounds how long a connection waits for a lock held by another one.
	busyTimeoutMillis = 5000
	fileMode          = 0o600
)

// Store is an open Home-Mandate database.
type Store struct {
	db *sql.DB
}

// Open opens or creates the database at path, which must be absolute, and applies all
// pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := validatePath(path); err != nil {
		return nil, err
	}
	if err := prepareFiles(path); err != nil {
		return nil, err
	}
	db, err := sql.Open(driverName, dataSourceName(path))
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("store: open: %w", err)
	}
	if err := migrate(ctx, db, embeddedMigrations()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// DB returns the connection pool. Packages own their tables, which they create through
// migrations in this package.
func (s *Store) DB() *sql.DB {
	return s.db
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// dataSourceName sets the pragmas for every connection the pool opens.
func dataSourceName(path string) string {
	return path + "?_txlock=immediate" +
		"&_pragma=foreign_keys(1)" +
		"&_pragma=journal_mode(WAL)" +
		"&_pragma=synchronous(FULL)" +
		"&_pragma=trusted_schema(0)" +
		fmt.Sprintf("&_pragma=busy_timeout(%d)", busyTimeoutMillis)
}

// validatePath accepts only absolute file paths that the driver cannot read as a URI or
// as parameters.
func validatePath(path string) error {
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "?#") || filepath.Clean(path) != path {
		return fmt.Errorf("%w: %q", ErrInvalidPath, path)
	}
	if info, err := os.Lstat(path); err == nil && info.IsDir() {
		return fmt.Errorf("%w: %q is a directory", ErrInvalidPath, path)
	}
	return nil
}

// prepareFiles checks the directory, creates the database file with owner-only
// permissions if needed and checks existing database, WAL and shared-memory files.
func prepareFiles(path string) error {
	dir := filepath.Dir(path)
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("store: database directory: %w", err)
	}
	if info.Mode().Perm()&0o002 != 0 {
		return fmt.Errorf("%w: %s is writable by others", ErrInsecurePermissions, dir)
	}

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, fileMode)
	switch {
	case err == nil:
		if err := f.Close(); err != nil {
			return fmt.Errorf("store: create database: %w", err)
		}
	case !errors.Is(err, fs.ErrExist):
		return fmt.Errorf("store: create database: %w", err)
	}

	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := checkFile(p); err != nil {
			return err
		}
	}
	return nil
}

// checkFile requires an existing file to be a regular file, not a symlink, that only
// its owner can access. A missing file is fine.
func checkFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrInsecurePermissions, path)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s has mode %o, want 600", ErrInsecurePermissions, path, info.Mode().Perm())
	}
	return nil
}
