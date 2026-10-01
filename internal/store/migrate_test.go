// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func migration(sql string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte("-- +goose Up\n" + sql + "\n")}
}

func openRaw(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	if err := prepareFiles(path); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open(driverName, dataSourceName(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func tableExists(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateRejectsInvalidSequences(t *testing.T) {
	tests := []struct {
		name string
		fsys fstest.MapFS
	}{
		{"no migrations", fstest.MapFS{}},
		{"gap", fstest.MapFS{
			"0001_a.sql": migration("CREATE TABLE a (x);"),
			"0003_c.sql": migration("CREATE TABLE c (x);"),
		}},
		{"does not start at 1", fstest.MapFS{"0002_b.sql": migration("CREATE TABLE b (x);")}},
		{"invalid name", fstest.MapFS{"0001-a.sql": migration("CREATE TABLE a (x);")}},
		{"upper case name", fstest.MapFS{"0001_A.sql": migration("CREATE TABLE a (x);")}},
		{"short version", fstest.MapFS{"1_a.sql": migration("CREATE TABLE a (x);")}},
		{"duplicate version", fstest.MapFS{
			"0001_a.sql": migration("CREATE TABLE a (x);"),
			"0001_b.sql": migration("CREATE TABLE b (x);"),
		}},
		{"unexpected file", fstest.MapFS{
			"0001_a.sql": migration("CREATE TABLE a (x);"),
			"README.md":  &fstest.MapFile{Data: []byte("x")},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := migrate(context.Background(), openRaw(t), tt.fsys)
			if !errors.Is(err, ErrMigrationSequence) {
				t.Errorf("error = %v, want ErrMigrationSequence", err)
			}
		})
	}
}

func TestMigrateRollsBackFailingMigration(t *testing.T) {
	db := openRaw(t)
	fsys := fstest.MapFS{
		"0001_a.sql": migration("CREATE TABLE a (x);"),
		"0002_b.sql": migration("CREATE TABLE b (x);\nINSERT INTO missing VALUES (1);"),
	}

	if err := migrate(context.Background(), db, fsys); err == nil {
		t.Fatal("migrate succeeded, want error")
	}

	if !tableExists(t, db, "a") {
		t.Error("migration 1 was not kept")
	}
	if tableExists(t, db, "b") {
		t.Error("failing migration 2 was not rolled back")
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM migration_checksums WHERE version = 2`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("checksum recorded for failed migration")
	}
}

func TestMigrateDetectsChangedMigration(t *testing.T) {
	db := openRaw(t)
	ctx := context.Background()
	if err := migrate(ctx, db, fstest.MapFS{"0001_a.sql": migration("CREATE TABLE a (x);")}); err != nil {
		t.Fatal(err)
	}

	err := migrate(ctx, db, fstest.MapFS{"0001_a.sql": migration("CREATE TABLE a (x, y);")})
	if !errors.Is(err, ErrMigrationChanged) {
		t.Errorf("error = %v, want ErrMigrationChanged", err)
	}
}

func TestMigrateRefusesDowngrade(t *testing.T) {
	db := openRaw(t)
	ctx := context.Background()
	two := fstest.MapFS{
		"0001_a.sql": migration("CREATE TABLE a (x);"),
		"0002_b.sql": migration("CREATE TABLE b (x);"),
	}
	if err := migrate(ctx, db, two); err != nil {
		t.Fatal(err)
	}

	err := migrate(ctx, db, fstest.MapFS{"0001_a.sql": two["0001_a.sql"]})
	if !errors.Is(err, ErrDatabaseTooNew) {
		t.Errorf("error = %v, want ErrDatabaseTooNew", err)
	}
}

func TestMigrateAppliesNewMigrationsIncrementally(t *testing.T) {
	db := openRaw(t)
	ctx := context.Background()
	one := fstest.MapFS{"0001_a.sql": migration("CREATE TABLE a (x);")}
	if err := migrate(ctx, db, one); err != nil {
		t.Fatal(err)
	}
	two := fstest.MapFS{
		"0001_a.sql": one["0001_a.sql"],
		"0002_b.sql": migration("CREATE TABLE b (x);"),
	}
	if err := migrate(ctx, db, two); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, two); err != nil {
		t.Fatalf("repeated migrate: %v", err)
	}

	var n int
	if err := db.QueryRow(`SELECT count(*) FROM migration_checksums`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 || !tableExists(t, db, "b") {
		t.Errorf("checksums = %d, table b = %v; want 2, true", n, tableExists(t, db, "b"))
	}
}

func TestMigrateRecordsMissingChecksumOfAppliedMigration(t *testing.T) {
	db := openRaw(t)
	ctx := context.Background()
	fsys := fstest.MapFS{"0001_a.sql": migration("CREATE TABLE a (x);")}
	if err := migrate(ctx, db, fsys); err != nil {
		t.Fatal(err)
	}
	// Simulates a crash between applying a migration and recording its checksum.
	if _, err := db.Exec(`DELETE FROM migration_checksums`); err != nil {
		t.Fatal(err)
	}

	if err := migrate(ctx, db, fsys); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM migration_checksums`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("checksums = %d, want 1", n)
	}
}

func TestEmbeddedMigrationsAreValid(t *testing.T) {
	if _, err := readMigrations(embeddedMigrations()); err != nil {
		t.Fatal(err)
	}
}
