// SPDX-License-Identifier: AGPL-3.0-or-later

package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/home-mandate/home-mandate/internal/store"
)

func openTemp(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "home-mandate.db")
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func reopen(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestOpenCreatesDatabaseWithOwnerOnlyPermissions(t *testing.T) {
	_, path := openTemp(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("permissions = %o, want 600", perm)
	}
}

func TestOpenAppliesMigrations(t *testing.T) {
	s, _ := openTemp(t)

	var count int
	err := s.DB().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = 'settings'`).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Errorf("settings table missing")
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.DB().Exec(`INSERT INTO settings (key, value, updated_at) VALUES ('k', 'v', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer again.Close()

	var value string
	if err := again.DB().QueryRow(`SELECT value FROM settings WHERE key = 'k'`).Scan(&value); err != nil {
		t.Fatalf("data lost after reopen: %v", err)
	}
}

func TestPragmasAreSetOnEveryConnection(t *testing.T) {
	s, _ := openTemp(t)
	ctx := context.Background()

	// Hold several connections at once so the pool has to open new ones.
	var conns []*sql.Conn
	for range 4 {
		c, err := s.DB().Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		var foreignKeys, synchronous, trustedSchema int
		var journalMode string
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA trusted_schema`).Scan(&trustedSchema); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journalMode); err != nil {
			t.Fatal(err)
		}
		if foreignKeys != 1 || synchronous != 2 || trustedSchema != 0 || journalMode != "wal" {
			t.Errorf("conn %d: foreign_keys=%d synchronous=%d trusted_schema=%d journal_mode=%s, want 1, 2 (FULL), 0, wal",
				i, foreignKeys, synchronous, trustedSchema, journalMode)
		}
	}
	for _, c := range conns {
		_ = c.Close()
	}
}

func TestOpenRejectsInvalidPaths(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name string
		path string
	}{
		{"empty", ""},
		{"relative", "home-mandate.db"},
		{"query separator", filepath.Join(dir, "db?_pragma=foreign_keys(0)")},
		{"uri", "file:" + filepath.Join(dir, "home-mandate.db")},
		{"memory", ":memory:"},
		{"directory", dir},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := store.Open(context.Background(), tt.path)
			if err == nil {
				s.Close()
				t.Fatal("Open succeeded, want error")
			}
			if !errors.Is(err, store.ErrInvalidPath) {
				t.Errorf("error = %v, want ErrInvalidPath", err)
			}
		})
	}
}

func TestOpenFailsForMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "home-mandate.db")
	if s, err := store.Open(context.Background(), path); err == nil {
		s.Close()
		t.Fatal("Open succeeded, want error")
	}
}

func TestOpenRejectsInsecurePermissions(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, dir, path string)
	}{
		{"directory writable by others", func(t *testing.T, dir, _ string) {
			chmod(t, dir, 0o777)
		}},
		{"directory writable by group", func(t *testing.T, dir, _ string) {
			chmod(t, dir, 0o770)
		}},
		{"database has a second hard link", func(t *testing.T, dir, path string) {
			writeFile(t, path, 0o600)
			if err := os.Link(path, filepath.Join(dir, "copy.db")); err != nil {
				t.Fatal(err)
			}
		}},
		{"database readable by group", func(t *testing.T, _, path string) {
			writeFile(t, path, 0o640)
		}},
		{"database readable by others", func(t *testing.T, _, path string) {
			writeFile(t, path, 0o604)
		}},
		{"wal file readable by others", func(t *testing.T, _, path string) {
			writeFile(t, path+"-wal", 0o644)
		}},
		{"database is a symlink", func(t *testing.T, dir, path string) {
			target := filepath.Join(dir, "elsewhere.db")
			writeFile(t, target, 0o600)
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"database is not a regular file", func(t *testing.T, _, path string) {
			if err := syscall.Mkfifo(path, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "home-mandate.db")
			tt.setup(t, dir, path)

			s, err := store.Open(context.Background(), path)
			if err == nil {
				s.Close()
				t.Fatal("Open succeeded, want error")
			}
			if !errors.Is(err, store.ErrInsecurePermissions) {
				t.Errorf("error = %v, want ErrInsecurePermissions", err)
			}
		})
	}
}

func TestOpenRejectsChangedMigrationChecksum(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.DB().Exec(`UPDATE migration_checksums SET checksum = 'sha256:tampered' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(context.Background(), path)
	if err == nil {
		again.Close()
		t.Fatal("Open succeeded, want error")
	}
	if !errors.Is(err, store.ErrMigrationChanged) {
		t.Errorf("error = %v, want ErrMigrationChanged", err)
	}
}

func TestOpenRejectsNewerDatabase(t *testing.T) {
	s, path := openTemp(t)
	if _, err := s.DB().Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, 1)`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	again, err := store.Open(context.Background(), path)
	if err == nil {
		again.Close()
		t.Fatal("Open succeeded, want error")
	}
	if !errors.Is(err, store.ErrDatabaseTooNew) {
		t.Errorf("error = %v, want ErrDatabaseTooNew", err)
	}
}

func TestOpenHonoursCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	path := filepath.Join(t.TempDir(), "home-mandate.db")
	if s, err := store.Open(ctx, path); err == nil {
		s.Close()
		t.Fatal("Open succeeded with a cancelled context")
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	s, _ := openTemp(t)
	const writers, readers, rounds = 8, 8, 25

	var wg sync.WaitGroup
	errs := make(chan error, writers+readers)
	for w := range writers {
		wg.Go(func() {
			for r := range rounds {
				_, err := s.DB().Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, '2026-10-01T00:00:00Z')
					ON CONFLICT (key) DO UPDATE SET value = excluded.value`, fmt.Sprintf("w%d", w), fmt.Sprint(r))
				if err != nil {
					errs <- err
					return
				}
			}
		})
	}
	for range readers {
		wg.Go(func() {
			for range rounds {
				var n int
				if err := s.DB().QueryRow(`SELECT count(*) FROM settings`).Scan(&n); err != nil {
					errs <- err
					return
				}
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}

	var n int
	if err := s.DB().QueryRow(`SELECT count(*) FROM settings`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != writers {
		t.Errorf("rows = %d, want %d", n, writers)
	}
}

func TestForeignKeysAreEnforced(t *testing.T) {
	s, _ := openTemp(t)
	db := s.DB()
	if _, err := db.Exec(`CREATE TABLE parent (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE child (parent_id INTEGER NOT NULL REFERENCES parent (id))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO child (parent_id) VALUES (42)`); err == nil {
		t.Error("insert with dangling foreign key succeeded")
	}
}

func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func writeFile(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, nil, mode); err != nil {
		t.Fatal(err)
	}
	chmod(t, path, mode) // umask may have removed bits
}
