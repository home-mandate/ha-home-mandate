// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
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

// upTo returns the embedded migrations up to and including version.
func upTo(t *testing.T, version int) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(embeddedMigrations(), ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries[:version] {
		data, err := fs.ReadFile(embeddedMigrations(), e.Name())
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = &fstest.MapFile{Data: data}
	}
	return out
}

// Migration 8 (decision F2) keeps every approver's phone as their first device, with
// critical requests; the UI channel starts switched off, and the database refuses
// ui_critical without ui.
func TestApproverChannelsMigrationKeepsPhones(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	if err := migrate(ctx, db, upTo(t, 7)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO approvers (user_id, notify_service, language, created_at)
		VALUES ('u1', 'mobile_app_pixel', 'de', '2026-10-01T00:00:00Z'), ('u2', 'mobile_app_iphone', '', '2026-10-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(ctx, db, embeddedMigrations()); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT a.user_id, d.notify_service, a.language, a.ui, a.ui_critical, d.critical
		FROM approvers a JOIN approver_devices d USING (user_id) ORDER BY a.user_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var user, service, lang string
		var ui, uiCritical, critical int
		if err := rows.Scan(&user, &service, &lang, &ui, &uiCritical, &critical); err != nil {
			t.Fatal(err)
		}
		got = append(got, fmt.Sprintf("%s %s %q ui=%d ui_critical=%d critical=%d", user, service, lang, ui, uiCritical, critical))
	}
	// The phones of before keep critical requests; the UI starts switched off.
	if want := []string{`u1 mobile_app_pixel "de" ui=0 ui_critical=0 critical=1`, `u2 mobile_app_iphone "" ui=0 ui_critical=0 critical=1`}; !slices.Equal(got, want) {
		t.Errorf("after migration %q, want %q", got, want)
	}
	if _, err := db.Exec(`UPDATE approvers SET ui_critical = 1 WHERE user_id = 'u1'`); err == nil {
		t.Error("ui_critical without ui accepted")
	}
	if _, err := db.Exec(`UPDATE approvers SET ui = 1, ui_critical = 1 WHERE user_id = 'u1'`); err != nil {
		t.Errorf("ui with ui_critical refused: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM approvers WHERE user_id = 'u1'`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM approver_devices WHERE user_id = 'u1'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("devices of a removed approver: %d, %v", n, err)
	}
}

// Migration 9 rebuilds the mandate tables without losing anything, keeps the foreign
// keys, allows a new mandate after a revoked one and still refuses two active mandates
// of one agent; the audit columns and the search table work under trusted_schema(0).
func TestUIMigrationKeepsMandates(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	if err := migrate(ctx, db, upTo(t, 8)); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO agents (client_id, display_name, status, created_at, created_by) VALUES ('hm-client:a', 'A', 'active', 't', 'u')`,
		`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
			VALUES ('m-a', 'hm-client:a', 'revoked', 'sha256:1', 60, 't1', 't2')`,
		`INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by) VALUES ('m-a', 'sha256:1', '{}', 't1', 'u')`,
		`INSERT INTO audit_log (seq, recorded_at, event, entry, digest) VALUES (1, 't', 'decision',
			'{"agent":{"client_id":"hm-client:a"},"request":{"resource":{"entity_id":"light.x","area":"k"}},"evaluation":{"decision":"deny","reason":"no_match"}}', 'd')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, db, embeddedMigrations()); err != nil {
		t.Fatal(err)
	}
	var id, status, name string
	var limit, versions int
	if err := db.QueryRow(`SELECT m.id, m.status, m.name, m.max_actions_per_hour, (SELECT count(*) FROM mandate_versions v WHERE v.mandate_id = m.id)
		FROM mandates m`).Scan(&id, &status, &name, &limit, &versions); err != nil {
		t.Fatal(err)
	}
	if id != "m-a" || status != "revoked" || name != "" || limit != 60 || versions != 1 {
		t.Errorf("mandate after migration: %s %s %q %d %d", id, status, name, limit, versions)
	}
	// A second mandate of the agent after the revoked one: allowed once, not twice active.
	if _, err := db.Exec(`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
		VALUES ('m-a-2', 'hm-client:a', 'active', 'sha256:2', 60, 't', 't')`); err != nil {
		t.Errorf("new mandate after a revoked one: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
		VALUES ('m-a-3', 'hm-client:a', 'active', 'sha256:3', 60, 't', 't')`); err == nil {
		t.Error("second active mandate of one agent accepted")
	}
	// Foreign keys survived the rebuild.
	if _, err := db.Exec(`INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by) VALUES ('m-none', 'x', '{}', 't', 'u')`); err == nil {
		t.Error("version of an unknown mandate accepted")
	}
	if _, err := db.Exec(`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
		VALUES ('m-b', 'hm-client:nobody', 'active', 'x', 1, 't', 't')`); err == nil {
		t.Error("mandate of an unknown agent accepted")
	}
	var client, entity, area, decision string
	if err := db.QueryRow(`SELECT client_id, entity_id, area, decision FROM audit_log WHERE seq = 1`).Scan(&client, &entity, &area, &decision); err != nil {
		t.Fatal(err)
	}
	if client != "hm-client:a" || entity != "light.x" || area != "k" || decision != "default" {
		t.Errorf("audit columns: %s %s %s %s", client, entity, area, decision)
	}
	if _, err := db.Exec(`INSERT INTO audit_search (seq, text) VALUES (1, 'x')`); err != nil {
		t.Errorf("search row: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO audit_search (seq, text) VALUES (2, 'x')`); err == nil {
		t.Error("search row without its entry accepted")
	}
}

// Migration 14 keeps where the rules of a version came from: versions stored before have
// no known origin; a version from a template names it and its digest, an edit names none.
func TestMandateVersionOriginMigration(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	if err := migrate(ctx, db, upTo(t, 13)); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO agents (client_id, display_name, status, created_at, created_by) VALUES ('hm-client:a', 'A', 'active', 't', 'u')`,
		`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
			VALUES ('m-a', 'hm-client:a', 'active', 'sha256:1', 60, 't1', 't2')`,
		`INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by) VALUES ('m-a', 'sha256:1', '{}', 't1', 'u')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, db, embeddedMigrations()); err != nil {
		t.Fatal(err)
	}
	var origin, name, digest string
	if err := db.QueryRow(`SELECT origin, template_name, template_digest FROM mandate_versions`).Scan(&origin, &name, &digest); err != nil {
		t.Fatal(err)
	}
	if origin != "unknown" || name != "" || digest != "" {
		t.Errorf("old version after migration: origin %q, template %q %q", origin, name, digest)
	}
	insert := func(origin, name, digest string) error {
		_, err := db.Exec(`INSERT INTO mandate_versions (mandate_id, digest, document, created_at, created_by, origin, template_name, template_digest)
			VALUES ('m-a', 'sha256:2', '{}', 't', 'u', ?, ?, ?)`, origin, name, digest)
		return err
	}
	for _, tc := range []struct {
		origin, name, digest string
		ok                   bool
	}{
		{"template", "voice", "sha256:abc", true},
		{"edit", "", "", true},
		{"unknown", "", "", true},
		{"template", "", "", false},
		{"template", "voice", "", false},
		{"edit", "voice", "sha256:abc", false},
		{"edit", "", "sha256:abc", false},
		{"unknown", "voice", "sha256:abc", false},
		{"other", "", "", false},
	} {
		if err := insert(tc.origin, tc.name, tc.digest); (err == nil) != tc.ok {
			t.Errorf("origin %q template %q digest %q: err = %v, want ok %v", tc.origin, tc.name, tc.digest, err, tc.ok)
		}
	}
}

// TestRemovalMigration: existing agents and mandates are not removed; a removed one
// stays revoked; audit entries are found by the mandate they name.
func TestRemovalMigration(t *testing.T) {
	ctx := context.Background()
	db := openRaw(t)
	if err := migrate(ctx, db, upTo(t, 14)); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`INSERT INTO agents (client_id, display_name, status, created_at, created_by) VALUES ('hm-client:a', 'A', 'revoked', 't', 'u')`,
		`INSERT INTO agents (client_id, display_name, status, created_at, created_by) VALUES ('hm-client:b', 'B', 'active', 't', 'u')`,
		`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
			VALUES ('m-a', 'hm-client:a', 'revoked', 'sha256:1', 60, 't1', 't2')`,
		`INSERT INTO mandates (id, client_id, status, current_digest, max_actions_per_hour, created_at, updated_at)
			VALUES ('m-b', 'hm-client:b', 'active', 'sha256:2', 60, 't1', 't2')`,
		`INSERT INTO audit_log (seq, recorded_at, event, entry, digest)
			VALUES (1, 't', 'mandate.revoked', '{"mandate":{"id":"m-a","digest":"sha256:1"}}', 'sha256:e')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	if err := migrate(ctx, db, embeddedMigrations()); err != nil {
		t.Fatal(err)
	}
	var removed int
	if err := db.QueryRow(`SELECT (SELECT count(*) FROM agents WHERE removed_at IS NOT NULL OR purged_at IS NOT NULL OR removed_by <> '')
		+ (SELECT count(*) FROM mandates WHERE removed_at IS NOT NULL OR purged_at IS NOT NULL OR removed_by <> '')`).Scan(&removed); err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Errorf("%d rows removed by the migration", removed)
	}
	var seq int
	if err := db.QueryRow(`SELECT seq FROM audit_log WHERE mandate_id = 'm-a'`).Scan(&seq); err != nil || seq != 1 {
		t.Errorf("entry by mandate: seq %d, %v", seq, err)
	}
	for _, tc := range []struct {
		stmt string
		ok   bool
	}{
		{`UPDATE agents SET removed_at = 't' WHERE client_id = 'hm-client:a'`, true},
		{`UPDATE agents SET removed_at = 't' WHERE client_id = 'hm-client:b'`, false},
		{`UPDATE agents SET status = 'active' WHERE client_id = 'hm-client:a'`, false},
		{`UPDATE mandates SET removed_at = 't' WHERE id = 'm-a'`, true},
		{`UPDATE mandates SET removed_at = 't' WHERE id = 'm-b'`, false},
		{`UPDATE mandates SET status = 'active' WHERE id = 'm-a'`, false},
	} {
		if _, err := db.Exec(tc.stmt); (err == nil) != tc.ok {
			t.Errorf("%s: err = %v, want ok %v", tc.stmt, err, tc.ok)
		}
	}
}

// releasedMigrations are the checksums of migrations that installations have applied.
// An applied migration must never change, not even in a comment: Migrate refuses to
// start with ErrMigrationChanged. Add every new migration here once it is installed
// anywhere; change nothing else.
var releasedMigrations = map[string]string{
	"0001_settings.sql":               "0297d128ce4f2b60f8e480e78da3f162672cfe0dc2d24c51420fcfc64f5ba100",
	"0002_audit_log.sql":              "ab6c29a63ab53fe583b65fc09ab54b56a3ddc2fedcd5c53b25c6256ac8adad10",
	"0003_agents.sql":                 "307b8e72b77c6d62c528f697a2f076ac99a35c9995116a89cfa7e509587ccec1",
	"0004_mandates.sql":               "c04cf59857fac44043918653bbd8ef50120b394723cd7d733b205e2244b1cfd8",
	"0005_oauth_tokens.sql":           "3b6ec7a20b6279d6dfa31fd32233f40bbc793a3d5f56a993683624be5e24079d",
	"0006_admission.sql":              "0758cf5f9d4e93d53220a8b05e952b03c8d652359cc4f4549f15d41cda56e387",
	"0007_approvers.sql":              "1a04af8976eeaf780036e4e90cc4fe68c89fb1b6ee0e07a02350f70a1090707f",
	"0008_approver_channels.sql":      "d094c2ec7ed9a395c3d9b0bacc637ea365614ca647bde1fc1f00337d929a999a",
	"0009_ui.sql":                     "1dd679e9b9c12c6b2f1c416a29bafb58cd352b8f27af29f05720fbacec395a18",
	"0010_critical_entities.sql":      "fca46aa8393927162c32b211044d0acb6a0fafdb5f973196c5dec76f352da5bd",
	"0011_mandate_versions.sql":       "6d154348cd038ede10fff7017712bf031e4873a6d91d44504c60051be191ed87",
	"0012_entity_renames.sql":         "aae965a7b15e1eaa9524b4da25db1dcff697eed8e0df8a4bb09478af4b7cc7ec",
	"0013_audit_directory.sql":        "f2dbbab4ba6ac677b45b72b5aaaf6239162ef8c2cab41700959278547c5d06d2",
	"0014_mandate_version_origin.sql": "74ef985cbb3a5f900c04ecdc8cac36c822d5069044055ebc8250fd0132b8d7b7",
}

func TestReleasedMigrationsAreFrozen(t *testing.T) {
	for name, want := range releasedMigrations {
		data, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			t.Errorf("%s: %v (a released migration must never be removed or renamed)", name, err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s changed (sha256 %s, released %s): installations refuse to start; restore it byte for byte", name, got, want)
		}
	}
}
