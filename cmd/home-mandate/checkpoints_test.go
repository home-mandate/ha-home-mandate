// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
	"github.com/home-mandate/ha-home-mandate/internal/i18n"
	"github.com/home-mandate/ha-home-mandate/internal/store"
)

func testStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, databaseFile))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st, dir
}

func noEnv(string) string { return "" }

func TestLoadSignerCreatesTheKeyOnceAndKeepsTheLogID(t *testing.T) {
	st, dir := testStore(t)
	ctx := context.Background()
	path := keyPath(dir, noEnv)
	if _, err := loadSigner(ctx, st, path, false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("loadSigner without creating = %v, want ErrNotExist", err)
	}
	first, err := loadSigner(ctx, st, path, true)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v; want a regular file with 0600", err, info.Mode())
	}
	// Written to a temporary file and moved into place: nothing else is left behind.
	if left, _ := filepath.Glob(filepath.Join(dir, ".audit-key-*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
	again, err := loadSigner(ctx, st, path, false)
	if err != nil || again.LogID != first.LogID || again.KeyID != first.KeyID || !again.Key.Equal(first.Key) {
		t.Errorf("second load differs: %v", err)
	}
	if len(first.LogID) != 36 || !strings.HasPrefix(first.KeyID, "log-") {
		t.Errorf("signer = %s %s", first.LogID, first.KeyID)
	}
	// A key outside the data directory, named by the environment.
	outside := filepath.Join(t.TempDir(), "key")
	if got := keyPath(dir, func(k string) string { return map[string]string{envAuditKeyFile: outside}[k] }); got != outside {
		t.Fatalf("keyPath = %s, want %s", got, outside)
	}
	other, err := loadSigner(ctx, st, outside, true)
	if err != nil || other.Key.Equal(first.Key) || other.LogID != first.LogID {
		t.Errorf("key from another place: %v", err)
	}
}

func TestLoadSignerRefusesABrokenKeyFile(t *testing.T) {
	st, dir := testStore(t)
	ctx := context.Background()
	path := filepath.Join(dir, auditKeyFile)
	for name, content := range map[string]string{"not base64": "???", "too short": "AQID"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSigner(ctx, st, path, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := loadSigner(ctx, st, filepath.Join(dir, "missing-directory", auditKeyFile), true); err == nil {
		t.Error("key in a directory that does not exist created")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := loadSigner(ctx, st, path, true); err == nil {
		t.Error("loadSigner without the database succeeded")
	}
}

// The key is read only from a regular file that nobody but its owner, the user
// Home-Mandate runs as, can read: no symbolic link, no directory, no group or world access.
func TestLoadSignerRefusesAnUnsafeKeyFile(t *testing.T) {
	st, dir := testStore(t)
	ctx := context.Background()
	good := filepath.Join(dir, auditKeyFile)
	if _, err := loadSigner(ctx, st, good, true); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.key")
	if err := os.Symlink(good, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSigner(ctx, st, link, false); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("symbolic link: %v", err)
	}
	if _, err := loadSigner(ctx, st, t.TempDir(), false); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Errorf("directory: %v", err)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644} {
		if err := os.Chmod(good, mode); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSigner(ctx, st, good, false); err == nil || !strings.Contains(err.Error(), "0600") {
			t.Errorf("mode %v: %v", mode, err)
		}
	}
	if err := os.Chmod(good, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSigner(ctx, st, good, false); err != nil {
		t.Errorf("back to 0600: %v", err)
	}
}

// The command line never creates the key, only serve on its first start; a log with
// checkpoints whose key is missing stops both instead of getting a new key that could
// not verify them.
func TestOnlyServeCreatesTheKey(t *testing.T) {
	c := newCLI(t)
	c.register("A")
	key := filepath.Join(c.envVars["HM_DATA_DIR"], auditKeyFile)
	out := c.mustRun("", "audit", "verify")
	if _, err := os.Lstat(key); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("audit verify created the key: %v", err)
	}
	if strings.Contains(out, "log_id=") {
		t.Errorf("log_id without a key: %q", out)
	}
	if code, _, errOut := c.run("", "audit", "key"); code != exitFailure || !strings.Contains(errOut, "serve") {
		t.Errorf("audit key without a key: exit %d, %q", code, errOut)
	}

	// serve creates it, then writes checkpoints.
	ctx := context.Background()
	s, err := openStore(ctx, c.envVars["HM_DATA_DIR"])
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	getenv := func(k string) string { return c.envVars[k] }
	if err := attachSigner(ctx, s, c.envVars["HM_DATA_DIR"], getenv, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.log.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(key); err != nil {
		t.Fatal(err)
	}
	for _, create := range []bool{false, true} {
		err := attachSigner(ctx, s, c.envVars["HM_DATA_DIR"], getenv, create)
		if err == nil || !strings.Contains(err.Error(), "missing although the audit log has checkpoints") {
			t.Errorf("create %v: %v", create, err)
		}
		if _, err := os.Lstat(key); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("create %v: a new key was created", create)
		}
	}
	if code, _, errOut := c.run("", "audit", "verify"); code != exitFailure || !strings.Contains(errOut, "has checkpoints") {
		t.Errorf("audit verify with checkpoints but no key: exit %d, %q", code, errOut)
	}
}

type fakeApprovers struct {
	list []approval.Approver
	err  error
}

func (f fakeApprovers) List(context.Context) ([]approval.Approver, error) { return f.list, f.err }

type sent struct {
	service string
	n       ha.Notification
}

func TestApproverAnchorTellsEveryDevice(t *testing.T) {
	var got []sent
	a := approverAnchor{
		approvers: fakeApprovers{list: []approval.Approver{
			{UserID: "u1", Language: "en", Devices: []approval.Device{{Service: "mobile_app_a"}, {Service: "mobile_app_broken"}}},
			{UserID: "u2", Devices: []approval.Device{{Service: "mobile_app_b"}}},
		}},
		notify: func(_ context.Context, service string, n ha.Notification) error {
			if service == "mobile_app_broken" {
				return errors.New("unreachable")
			}
			got = append(got, sent{service, n})
			return nil
		},
		language: func() i18n.Lang { return i18n.DE },
	}
	pos := audit.Position{LogID: "0198f1c2-7c3a-7000-8000-0000000000aa", Seq: 42, Digest: "sha256:" + strings.Repeat("ab", 32)}
	if err := a.Publish(context.Background(), pos); err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].service != "mobile_app_a" || got[1].service != "mobile_app_b" {
		t.Fatalf("sent = %+v", got)
	}
	for _, s := range got {
		if !strings.Contains(s.n.Message, "42") || !strings.Contains(s.n.Message, "abababababababab") ||
			strings.Contains(s.n.Message, strings.Repeat("ab", 32)) || !strings.Contains(s.n.Message, pos.LogID) || s.n.Title == "" {
			t.Errorf("%s: %+v", s.service, s.n)
		}
	}
	if !strings.Contains(got[0].n.Title, "audit log") || !strings.Contains(got[1].n.Title, "Protokoll") {
		t.Errorf("languages: %q, %q", got[0].n.Title, got[1].n.Title)
	}
	a.approvers = fakeApprovers{}
	if err := a.Publish(context.Background(), pos); !errors.Is(err, errNoApprover) {
		t.Errorf("without approvers: %v", err)
	}
	a.approvers = fakeApprovers{err: errors.New("database gone")}
	if err := a.Publish(context.Background(), pos); err == nil {
		t.Error("store error swallowed")
	}
	if got := shortDigest("sha256:abc"); got != "abc" {
		t.Errorf("shortDigest of a short digest = %q", got)
	}
}

type fakeAnchor struct {
	positions []audit.Position
	err       error
}

func (f *fakeAnchor) Publish(_ context.Context, pos audit.Position) error {
	if f.err != nil {
		return f.err
	}
	f.positions = append(f.positions, pos)
	return nil
}

func decisionEntry() audit.Entry {
	return audit.Entry{Event: audit.EventEmergencyStopActivated, Actor: &audit.Actor{Kind: audit.ActorUser, ID: "user-1"}}
}

func TestCheckpointerAnchorsOncePerInterval(t *testing.T) {
	st, dir := testStore(t)
	ctx := context.Background()
	log := audit.New(st.DB(), "household:t")
	signer, err := loadSigner(ctx, st, keyPath(dir, noEnv), true)
	if err != nil {
		t.Fatal(err)
	}
	log.SetSigner(signer)
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	out := &fakeAnchor{}
	c := checkpointer{log: log, settings: st, anchor: out, now: func() time.Time { return now },
		logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	c.once(ctx) // empty log: nothing to do
	if len(out.positions) != 0 {
		t.Fatalf("anchored an empty log: %+v", out.positions)
	}
	if _, err := log.Append(ctx, decisionEntry()); err != nil {
		t.Fatal(err)
	}
	c.once(ctx)
	if len(out.positions) != 1 || out.positions[0].Seq != 1 || out.positions[0].LogID != signer.LogID {
		t.Fatalf("first checkpoint: %+v", out.positions)
	}
	// New entries an hour later: a checkpoint is written, but nobody is told again yet.
	now = now.Add(time.Hour)
	if _, err := log.Append(ctx, decisionEntry()); err != nil {
		t.Fatal(err)
	}
	c.once(ctx)
	if len(out.positions) != 1 {
		t.Errorf("anchored again within the interval: %+v", out.positions)
	}
	if r, err := log.Verify(ctx); err != nil || !r.Valid || r.AnchoredSeq != 3 {
		t.Errorf("Verify = %+v, %v; want anchored up to 3", r, err)
	}
	now = now.Add(anchorInterval)
	if _, err := log.Append(ctx, decisionEntry()); err != nil {
		t.Fatal(err)
	}
	c.once(ctx)
	if len(out.positions) != 2 || out.positions[1].Seq != 5 {
		t.Errorf("after the interval: %+v", out.positions)
	}
	// An anchor that fails is tried again with the next checkpoint.
	out.err = errors.New("nobody reachable")
	now = now.Add(anchorInterval)
	_, _ = log.Append(ctx, decisionEntry())
	c.once(ctx)
	out.err = nil
	_, _ = log.Append(ctx, decisionEntry())
	c.once(ctx)
	if len(out.positions) != 3 {
		t.Errorf("after a failed anchor: %+v", out.positions)
	}
	// Without an anchor and without a signer nothing breaks.
	c.anchor = nil
	_, _ = log.Append(ctx, decisionEntry())
	c.once(ctx)
	log.SetSigner(nil)
	c.once(ctx)
}

func TestCheckpointerRunStopsWithTheContext(t *testing.T) {
	st, dir := testStore(t)
	log := audit.New(st.DB(), "household:t")
	signer, err := loadSigner(context.Background(), st, keyPath(dir, noEnv), true)
	if err != nil {
		t.Fatal(err)
	}
	log.SetSigner(signer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c := checkpointer{log: log, settings: st, now: time.Now, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	go func() { c.run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop")
	}
}
