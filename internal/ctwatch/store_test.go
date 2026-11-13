// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/store"
)

var t0 = time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)

func newStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewStore(st.DB()), st.DB()
}

func digestOf(s string) Digest { return sha256.Sum256([]byte(s)) }

func TestWatchSinceBeginsOnce(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	since, first, err := s.WatchSince(ctx, "home.example", t0)
	if err != nil || !first || !since.Equal(t0) {
		t.Fatalf("%v %v %v", since, first, err)
	}
	since, first, err = s.WatchSince(ctx, "home.example", t0.Add(time.Hour))
	if err != nil || first || !since.Equal(t0) {
		t.Fatalf("second start: %v %v %v", since, first, err)
	}
}

func TestKeyHistory(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	if k, err := s.Keys(ctx); err != nil || k.HasCurrent || len(k.Earlier) != 0 {
		t.Fatalf("empty: %+v %v", k, err)
	}
	a, b := digestOf("a"), digestOf("b")
	if changed, err := s.ObserveKey(ctx, a, t0); err != nil || !changed {
		t.Fatalf("first key: %v %v", changed, err)
	}
	if changed, err := s.ObserveKey(ctx, a, t0.Add(time.Hour)); err != nil || changed {
		t.Fatalf("same key: %v %v", changed, err)
	}
	replaced := t0.Add(2 * time.Hour)
	if changed, err := s.ObserveKey(ctx, b, replaced); err != nil || !changed {
		t.Fatalf("new key: %v %v", changed, err)
	}
	// A key that comes back is current again; its earlier replacement stays as it was.
	back := t0.Add(3 * time.Hour)
	if _, err := s.ObserveKey(ctx, a, back); err != nil {
		t.Fatal(err)
	}
	k, err := s.Keys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []EarlierKey{{PublicKey: a, Replaced: replaced}, {PublicKey: b, Replaced: back}}
	if !k.HasCurrent || k.Current != a || !slices.Equal(k.Earlier, want) {
		t.Fatalf("keys %+v", k)
	}
	// The database refuses to rewrite the history.
	if _, err := db.Exec(`UPDATE ct_keys SET replaced = ? WHERE replaced IS NOT NULL`, formatTime(t0)); err == nil {
		t.Fatal("an earlier key's replacement was changed")
	}
	if _, err := db.Exec(`INSERT INTO ct_keys (public_key, since) VALUES (?, ?)`, digestOf("c").String(), formatTime(t0)); err == nil {
		t.Fatal("two current keys")
	}
}

func parsedCert(name string, logged time.Time) Parsed {
	return Parsed{Cert: Cert{Digest: digestOf("tbs-" + name), Names: []string{name}, NotBefore: t0, NotAfter: t0.Add(90 * 24 * time.Hour),
		PublicKey: digestOf("key-" + name), Logged: logged}, Issuer: "CN=Test CA", Serial: "2a"}
}

func TestCertsKeepTheEarliestLogTime(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	p := parsedCert("home.example", time.Time{})
	if isNew, err := s.PutCert(ctx, p, t0); err != nil || !isNew {
		t.Fatalf("%v %v", isNew, err)
	}
	later, earlier := t0.Add(time.Hour), t0.Add(time.Minute)
	for _, logged := range []time.Time{{}, later, earlier, later} {
		p.Logged = logged
		if isNew, err := s.PutCert(ctx, p, t0.Add(time.Hour)); err != nil || isNew {
			t.Fatalf("%v %v", isNew, err)
		}
	}
	certs, err := s.Certs(ctx)
	if err != nil || len(certs) != 1 {
		t.Fatalf("%d %v", len(certs), err)
	}
	c := certs[0]
	if !c.Logged.Equal(earlier) || !c.FirstSeen.Equal(t0) || !c.NotifiedAt.IsZero() || c.Issuer != "CN=Test CA" || c.Serial != "2a" ||
		!slices.Equal(c.Names, p.Names) || c.Digest != p.Digest || c.PublicKey != p.PublicKey || !c.NotAfter.Equal(p.NotAfter) {
		t.Fatalf("stored %+v", c)
	}
	if err := s.MarkNotified(ctx, []Digest{c.Digest}, later); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkNotified(ctx, []Digest{c.Digest}, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	certs, _ = s.Certs(ctx)
	if !certs[0].NotifiedAt.Equal(later) {
		t.Fatalf("notified %v, the first time counts", certs[0].NotifiedAt)
	}
}

func TestLongIssuerAndSerialAreCut(t *testing.T) {
	s, _ := newStore(t)
	p := parsedCert("a.example", t0)
	p.Issuer, p.Serial = strings.Repeat("ä", maxIssuerText), strings.Repeat("f", 500)
	if _, err := s.PutCert(context.Background(), p, t0); err != nil {
		t.Fatal(err)
	}
	certs, _ := s.Certs(context.Background())
	if len(certs[0].Issuer) > maxIssuerText || len(certs[0].Serial) != maxSerialText || !strings.HasPrefix(p.Issuer, certs[0].Issuer) {
		t.Fatalf("issuer %d bytes, serial %d", len(certs[0].Issuer), len(certs[0].Serial))
	}
}

func TestAcknowledgementsStayAsTheyWere(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	d := digestOf("tbs-x")
	if err := s.Acknowledge(ctx, Ack{Digest: d, By: "u1", Via: ViaPush, At: t0}); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(ctx, Ack{Digest: d, By: "u2", Via: ViaUI, At: t0.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	acks, err := s.Acks(ctx)
	if err != nil || len(acks) != 1 || acks[0] != (Ack{Digest: d, By: "u1", Via: ViaPush, At: t0}) {
		t.Fatalf("%+v %v", acks, err)
	}
	if _, err := db.Exec(`INSERT INTO ct_acks (digest, by, via, at) VALUES (?, 'u', 'mail', ?)`, digestOf("y").String(), formatTime(t0)); err == nil {
		t.Fatal("unknown way of confirming stored")
	}
}

func TestSourceState(t *testing.T) {
	s, _ := newStore(t)
	ctx := context.Background()
	if st, err := s.Source(ctx, "certspotter", "a.example"); err != nil || st != (SourceState{}) {
		t.Fatalf("%+v %v", st, err)
	}
	if err := s.Failed(ctx, "certspotter", "a.example", errors.New(strings.Repeat("x", 1000)), t0); err != nil {
		t.Fatal(err)
	}
	st, err := s.Source(ctx, "certspotter", "a.example")
	if err != nil || !st.LastFailure.Equal(t0) || len(st.Failure) != maxFailureText || st.Cursor != "" || !st.LastSuccess.IsZero() {
		t.Fatalf("%+v %v", st, err)
	}
	if err := s.Succeeded(ctx, "certspotter", "a.example", "123", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.Failed(ctx, "certspotter", "a.example", errors.New("timeout"), t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Source(ctx, "certspotter", "a.example")
	if st.Cursor != "123" || !st.LastSuccess.Equal(t0.Add(time.Hour)) || st.Failure != "timeout" {
		t.Fatalf("a failure keeps cursor and last success: %+v", st)
	}
	if err := s.Succeeded(ctx, "certspotter", "a.example", "124", t0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	st, _ = s.Source(ctx, "certspotter", "a.example")
	if st.Failure != "" || st.Cursor != "124" || !st.LastFailure.Equal(t0.Add(2*time.Hour)) {
		t.Fatalf("a success clears the failure text: %+v", st)
	}
	if err := s.Succeeded(ctx, "elsewhere", "a.example", "", t0); err == nil {
		t.Fatal("unknown source stored")
	}
}

// TestStoreFailsClosed: a damaged row or a closed database is an error, never an empty
// answer that would hide a certificate or an acknowledgement.
func TestStoreFailsClosed(t *testing.T) {
	s, db := newStore(t)
	ctx := context.Background()
	if _, err := s.PutCert(ctx, parsedCert("a.example", t0), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE ct_certs SET not_after = 'yesterday'`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Certs(ctx); !errors.Is(err, ErrStore) {
		t.Fatalf("damaged time: %v", err)
	}
	if _, err := db.Exec(`UPDATE ct_certs SET not_after = ?, digest = ?`, formatTime(t0), strings.Repeat("!", 43)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Certs(ctx); !errors.Is(err, ErrStore) {
		t.Fatalf("damaged digest: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ct_keys (public_key, since) VALUES (?, ?)`, strings.Repeat("!", 43), formatTime(t0)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Keys(ctx); !errors.Is(err, ErrStore) {
		t.Fatalf("damaged key: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO ct_acks (digest, by, via, at) VALUES (?, 'u', 'ui', 'never')`, digestOf("z").String()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Acks(ctx); !errors.Is(err, ErrStore) {
		t.Fatalf("damaged acknowledgement: %v", err)
	}
	_ = db.Close()
	for name, call := range map[string]func() error{
		"watch":    func() error { _, _, err := s.WatchSince(ctx, "a.example", t0); return err },
		"observe":  func() error { _, err := s.ObserveKey(ctx, digestOf("k"), t0); return err },
		"put":      func() error { _, err := s.PutCert(ctx, parsedCert("b.example", t0), t0); return err },
		"notified": func() error { return s.MarkNotified(ctx, nil, t0) },
		"ack":      func() error { return s.Acknowledge(ctx, Ack{Digest: digestOf("x"), By: "u", Via: ViaUI, At: t0}) },
		"source":   func() error { _, err := s.Source(ctx, "crtsh", "a.example"); return err },
	} {
		if err := call(); !errors.Is(err, ErrStore) {
			t.Errorf("%s on a closed database: %v", name, err)
		}
	}
}

func TestTruncateKeepsRunes(t *testing.T) {
	if got := truncate("aä", 2); got != "a" {
		t.Fatalf("%q", got)
	}
	if got := truncate("abc", 5); got != "abc" {
		t.Fatalf("%q", got)
	}
}
