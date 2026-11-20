// SPDX-License-Identifier: AGPL-3.0-or-later

package ctwatch

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// auditOut is the exported audit log of the harness.
func (h *harness) auditOut() string {
	h.t.Helper()
	var buf bytes.Buffer
	if err := h.audit.Export(context.Background(), &buf); err != nil {
		h.t.Fatal(err)
	}
	return buf.String()
}

// SPEC-v0 sections 9.2 and 11.7 item 7 (audit/v1): a certificate that raises an alarm, is
// not valid yet, or was a past exposure is recorded once for each certificate and status,
// not at every check, with what identifies it; only its names that match the host.
func TestUnexpectedCertificatesAreRecordedOncePerStatus(t *testing.T) {
	h := newHarness(t)
	soon := foreign("soon", t0.Add(-time.Hour), t0.Add(2*time.Hour))
	soon.Names = []string{watchedName, "other.example.net"}
	later := foreign("later", t0.Add(48*time.Hour), t0.Add(90*24*time.Hour))
	h.spotter.add(soon, later)
	h.round()
	h.round()
	out := h.auditOut()
	if n := strings.Count(out, `"event":"cert.unexpected"`); n != 2 {
		t.Fatalf("%d cert.unexpected after two rounds, want 2:\n%s", n, out)
	}
	alarm := `"certificate":{"digest":"` + soon.Digest.String() + `","issuer":"CN=Evil CA [click](https://x.example)","logged":"2026-11-13T09:00:00.000Z",` +
		`"names":["` + watchedName + `"],"not_after":"2026-11-13T12:00:00.000Z","not_before":"2026-11-13T09:00:00.000Z",` +
		`"public_key":"` + soon.PublicKey.String() + `","status":"alarm"}`
	if !strings.Contains(out, alarm) || !strings.Contains(out, `"digest":"`+later.Digest.String()) || !strings.Contains(out, `"status":"not_yet_valid"`) {
		t.Errorf("audit log:\n%s", out)
	}
	// Expired since: the same certificate is now a past exposure, recorded once more.
	h.clock.advance(3 * time.Hour)
	h.round()
	h.round()
	out = h.auditOut()
	if n := strings.Count(out, `"status":"past_exposure"`); n != 1 || strings.Count(out, `"event":"cert.unexpected"`) != 3 {
		t.Errorf("after the expiry:\n%s", out)
	}
	if r, err := h.audit.Verify(context.Background()); err != nil || !r.Valid {
		t.Errorf("Verify = %+v, %v", r, err)
	}
}

// An acknowledgement is recorded with the administrator who confirmed it and the digest.
func TestAnAcknowledgementIsRecorded(t *testing.T) {
	h := newHarness(t)
	pending, p := ackFor(t, h)
	if err := h.w.AnswerAck(context.Background(), pending.ID, "anna", true); err != nil {
		t.Fatal(err)
	}
	want := `"actor":{"id":"anna","kind":"user"},"certificate":{"digest":"` + p.Digest.String() + `"}`
	if out := h.auditOut(); strings.Count(out, `"event":"cert.acknowledged"`) != 1 || !strings.Contains(out, want) {
		t.Errorf("audit log:\n%s", out)
	}
}

// The state of monitoring is recorded at every change: ok while both sources answer,
// limited with one, unavailable with none for 24 hours; the first entry has no previous
// state, and nothing is recorded before a source answered or 24 hours passed.
func TestTheStateOfMonitoringIsRecordedWhenItChanges(t *testing.T) {
	h := newHarness(t)
	states := func() []string {
		var out []string
		for _, line := range strings.Split(h.auditOut(), "\n") {
			if i := strings.Index(line, `"ct":`); i >= 0 {
				out = append(out, line[i+5:i+strings.Index(line[i:], "}")+1])
			}
		}
		return out
	}
	h.spotter.fail(errors.New("down"))
	h.crtsh.fail(errors.New("down"))
	h.round()
	if got := states(); len(got) != 0 {
		t.Fatalf("recorded before any verdict: %v", got)
	}
	h.crtsh.fail(nil)
	h.round()
	h.spotter.fail(nil)
	h.round()
	h.round()
	h.spotter.fail(errors.New("down"))
	h.clock.advance(Unavailable)
	h.round()
	h.crtsh.fail(errors.New("down"))
	h.clock.advance(Unavailable)
	h.round()
	want := []string{
		`{"status":"limited"}`,
		`{"previous_status":"limited","status":"ok"}`,
		`{"previous_status":"ok","status":"limited"}`,
		`{"previous_status":"limited","status":"unavailable"}`,
	}
	if got := states(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("ct.status:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// What cert.unexpected records of a certificate stays within the bounds of the audit
// format: at most 16 names, an issuer of at most 1024 characters, and a time of logging
// also when no source told it (the time the monitor first saw the certificate).
func TestARecordedCertificateStaysWithinTheFormat(t *testing.T) {
	h := newHarness(t)
	// Names match without regard to case: 20 spellings of the watched name.
	var names []string
	for i := range 20 {
		b := []byte(watchedName)
		for bit := range 5 {
			if i&(1<<bit) != 0 {
				b[bit] -= 'a' - 'A'
			}
		}
		names = append(names, string(b))
	}
	d := decided{Stored: Stored{Parsed: Parsed{Cert: Cert{Digest: digestOf("d"), Names: append([]string{watchedName, "other.example.net"}, names...),
		NotBefore: t0, NotAfter: t0.Add(time.Hour), PublicKey: digestOf("k")}, Issuer: strings.Repeat("ä", 1500)}, FirstSeen: t0.Add(time.Minute)},
		Result: Alarm}
	c := h.w.recordedCert(d, "alarm")
	if len(c.Names) != maxRecordedNames || c.Names[0] != watchedName {
		t.Errorf("names = %v", c.Names)
	}
	if n := len([]rune(c.Issuer)); n != maxIssuerRunes || !strings.HasSuffix(c.Issuer, "…") {
		t.Errorf("issuer of %d runes", n)
	}
	if !c.Logged.Equal(t0.Add(time.Minute)) {
		t.Errorf("logged = %v, want the time first seen", c.Logged)
	}
}

// A store that fails while recording, or a watcher without an audit log, records nothing
// and keeps monitoring: the failure is logged, and the next round tries again.
func TestRecordingFailuresAreLogged(t *testing.T) {
	h := newHarness(t)
	h.spotter.add(foreign("a", t0, t0.Add(90*24*time.Hour)))
	h.w.cfg.Audit = failingRecorder{}
	h.round()
	if !strings.Contains(h.logs.String(), "audit log write failed") {
		t.Errorf("not logged:\n%s", h.logs)
	}
	h.w.cfg.Audit = nil
	h.clock.advance(Interval)
	h.round()
	if h.w.recordAck(context.Background(), digestOf("a"), "anna") != nil {
		t.Error("an acknowledgement is recorded without an audit log")
	}
}

type failingRecorder struct{}

func (failingRecorder) AppendTx(context.Context, *sql.Tx, audit.Entry) (int64, error) {
	return 0, errors.New("log unavailable")
}
func (failingRecorder) RecordCTStatus(context.Context, string) error {
	return errors.New("log unavailable")
}

// A certificate is anyone's to make: one without an issuer is recorded all the same, with a
// stand-in, so that its alarm cannot be kept out of the log.
func TestACertificateWithoutAnIssuerIsRecorded(t *testing.T) {
	h := newHarness(t)
	bare := foreign("bare", t0.Add(-time.Hour), t0.Add(90*24*time.Hour))
	bare.Issuer = ""
	h.spotter.add(bare)
	h.round()
	if out := h.auditOut(); !hasEntry(out, `"event":"cert.unexpected"`, `"digest":"`+bare.Digest.String()+`"`, `"issuer":"`+noIssuer+`"`) {
		t.Errorf("audit log:\n%s", out)
	}
	if strings.Contains(h.logs.String(), "audit log write failed") {
		t.Errorf("not recorded:\n%s", h.logs)
	}
	// Names that do not cover the watched name are recorded rather than none.
	d := decided{Stored: Stored{Parsed: Parsed{Cert: Cert{Digest: digestOf("d"), Names: []string{"other.example.net"}}}}, Result: Alarm}
	if c := h.w.recordedCert(d, "alarm"); len(c.Names) != 1 || c.Names[0] != "other.example.net" {
		t.Errorf("names = %v", c.Names)
	}
}

func hasEntry(log string, parts ...string) bool {
	for _, line := range strings.Split(log, "\n") {
		all := true
		for _, p := range parts {
			all = all && strings.Contains(line, p)
		}
		if all {
			return true
		}
	}
	return false
}
