// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strconv"

	"github.com/pressly/goose/v3"
)

var (
	// ErrMigrationSequence means the embedded migrations are not numbered 0001, 0002, …
	// without gaps, or a file name does not follow NNNN_name.sql.
	ErrMigrationSequence = errors.New("store: invalid migration sequence")
	// ErrMigrationChanged means an applied migration differs from the embedded one.
	ErrMigrationChanged = errors.New("store: applied migration was changed")
	// ErrDatabaseTooNew means the database was migrated by a newer binary.
	ErrDatabaseTooNew = errors.New("store: database is newer than this binary")
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

var migrationName = regexp.MustCompile(`^([0-9]{4})_[a-z0-9_]+\.sql$`)

func embeddedMigrations() fs.FS {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		panic(err) // the directory is embedded at build time
	}
	return sub
}

// migrationFile is one embedded migration; index i holds version i+1.
type migrationFile struct {
	version  int64
	checksum string
}

// readMigrations checks names and numbering and computes the checksums.
func readMigrations(fsys fs.FS) ([]migrationFile, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMigrationSequence, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("%w: no migrations", ErrMigrationSequence)
	}
	files := make([]migrationFile, 0, len(entries))
	for i, e := range entries { // ReadDir sorts by name
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			return nil, fmt.Errorf("%w: unexpected file %q", ErrMigrationSequence, e.Name())
		}
		version, _ := strconv.ParseInt(m[1], 10, 64) // four digits always parse
		if version != int64(i+1) {
			return nil, fmt.Errorf("%w: %q, want version %04d", ErrMigrationSequence, e.Name(), i+1)
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("store: read migration: %w", err)
		}
		sum := sha256.Sum256(data)
		files = append(files, migrationFile{version: version, checksum: "sha256:" + hex.EncodeToString(sum[:])})
	}
	return files, nil
}

// migrate applies pending migrations from fsys, each in its own transaction, guarded by
// the checks goose does not do: no downgrade and no change to applied migrations.
func migrate(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	files, err := readMigrations(fsys)
	if err != nil {
		return err
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, db, fsys, goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return fmt.Errorf("store: migrations: %w", err)
	}

	current, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("store: database version: %w", err)
	}
	latest := files[len(files)-1].version
	if current > latest {
		return fmt.Errorf("%w: database version %d, binary knows up to %d", ErrDatabaseTooNew, current, latest)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS migration_checksums (
		version  INTEGER PRIMARY KEY NOT NULL,
		checksum TEXT NOT NULL
	) STRICT`); err != nil {
		return fmt.Errorf("store: checksum table: %w", err)
	}
	if err := verifyChecksums(ctx, db, files[:current]); err != nil {
		return err
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	return recordChecksums(ctx, db, files)
}

// verifyChecksums compares recorded checksums of applied migrations with the embedded ones.
// A missing record is tolerated: it is written after the migration was applied, and a
// crash in between must not make the database unusable.
func verifyChecksums(ctx context.Context, db *sql.DB, applied []migrationFile) error {
	for _, f := range applied {
		var recorded string
		err := db.QueryRowContext(ctx, `SELECT checksum FROM migration_checksums WHERE version = ?`, f.version).Scan(&recorded)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("store: read checksum: %w", err)
		}
		if recorded != f.checksum {
			return fmt.Errorf("%w: version %d", ErrMigrationChanged, f.version)
		}
	}
	return nil
}

func recordChecksums(ctx context.Context, db *sql.DB, files []migrationFile) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: record checksums: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, f := range files {
		if _, err := tx.ExecContext(ctx, `INSERT INTO migration_checksums (version, checksum) VALUES (?, ?)
			ON CONFLICT (version) DO NOTHING`, f.version, f.checksum); err != nil {
			return fmt.Errorf("store: record checksums: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: record checksums: %w", err)
	}
	return nil
}
