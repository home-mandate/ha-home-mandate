// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"context"
	"errors"
	"io"
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
	first, err := loadSigner(ctx, st, dir, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, auditKeyFile))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: %v, mode %v; want 0600", err, info.Mode().Perm())
	}
	again, err := loadSigner(ctx, st, dir, noEnv)
	if err != nil || again.LogID != first.LogID || again.KeyID != first.KeyID || !again.Key.Equal(first.Key) {
		t.Errorf("second load differs: %v", err)
	}
	if len(first.LogID) != 36 || !strings.HasPrefix(first.KeyID, "log-") {
		t.Errorf("signer = %s %s", first.LogID, first.KeyID)
	}
	// A key outside the data directory, named by the environment.
	outside := filepath.Join(t.TempDir(), "key")
	other, err := loadSigner(ctx, st, dir, func(k string) string {
		if k == envAuditKeyFile {
			return outside
		}
		return ""
	})
	if err != nil || other.Key.Equal(first.Key) || other.LogID != first.LogID {
		t.Errorf("key from another place: %v", err)
	}
}

func TestLoadSignerRefusesABrokenKeyFile(t *testing.T) {
	st, dir := testStore(t)
	ctx := context.Background()
	for name, content := range map[string]string{"not base64": "???", "too short": "AQID"} {
		if err := os.WriteFile(filepath.Join(dir, auditKeyFile), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSigner(ctx, st, dir, noEnv); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := loadSigner(ctx, st, filepath.Join(dir, "missing-directory"), noEnv); err == nil {
		t.Error("key in a directory that does not exist created")
	}
	if err := os.Remove(filepath.Join(dir, auditKeyFile)); err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := loadSigner(ctx, st, dir, noEnv); err == nil {
		t.Error("loadSigner without the database succeeded")
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
	signer, err := loadSigner(ctx, st, dir, noEnv)
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
	signer, err := loadSigner(context.Background(), st, dir, noEnv)
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
