// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/ha"
)

func TestSession(t *testing.T) {
	h := newHarness(t)
	var s wireSession
	h.ok(http.MethodGet, "/api/session", nil, &s)
	if s.User.ID != adminID || s.User.Name != "Markus" || s.CSRFToken == "" || s.Language != nil ||
		s.Household.TimeZone != "Europe/Berlin" || s.Household.Language != "de" || s.Household.UnitSystem["temperature"] != "°C" ||
		s.Household.UnitSystem["pressure"] != "" || len(s.Household.UnitSystem) != len(unitKeys) {
		t.Errorf("session = %+v", s)
	}
	if !h.srv.validCSRF(adminID, s.CSRFToken) || h.srv.validCSRF(annaID, s.CSRFToken) {
		t.Error("the session's token is not the user's")
	}
	h.ok(http.MethodPut, "/api/session/language", map[string]any{"language": "en"}, &s)
	if s.Language == nil || *s.Language != "en" {
		t.Errorf("language = %v", s.Language)
	}
	// Per user: Anna still has none.
	var anna wireSession
	h.ok(http.MethodGet, "/api/session", nil, &anna, as(annaID))
	if anna.Language != nil || anna.User.Name != "Anna" {
		t.Errorf("anna = %+v", anna)
	}
	h.ok(http.MethodPut, "/api/session/language", map[string]any{"language": nil}, &s)
	if s.Language != nil {
		t.Errorf("language after null = %v", *s.Language)
	}
	for _, bad := range []any{"fr", "", "DE", 1} {
		if r := h.do(http.MethodPut, "/api/session/language", map[string]any{"language": bad}); r.errCode() != codeInvalidInput || r.field() != "/language" {
			t.Errorf("language %v = %d %s", bad, r.code, r.body)
		}
	}
	// A stored value that is no UI language is ignored.
	_ = h.st.SetSetting(t.Context(), languageKeyPrefix+adminID, "xx")
	h.ok(http.MethodGet, "/api/session", nil, &s)
	if s.Language != nil {
		t.Errorf("unknown stored language = %v", *s.Language)
	}
	// Before Home Assistant answered: UTC, and the ID when the name is unknown.
	h.status.TimeZone = ""
	h.ha.set(func(f *fakeHA) {
		f.users = append(f.users, ha.AuthUser{ID: "noname0000000000000000000000000", IsOwner: true, IsActive: true})
	})
	h.now.Add(usersTTL)
	h.ok(http.MethodGet, "/api/session", nil, &s, as("noname0000000000000000000000000"))
	if s.Household.TimeZone != "UTC" || s.User.Name != "noname0000000000000000000000000" {
		t.Errorf("session = %+v", s)
	}
}

func TestSystem(t *testing.T) {
	h := newHarness(t)
	h.putApprover(approval.Approver{UserID: adminID, Devices: []approval.Device{{Service: "mobile_app_iphone_von_markus", Critical: true}}})
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.Mode != "app" || sys.Version != "0.1.0" || sys.Commit != "abc123" || sys.ServerTime != "2026-10-03T10:00:00.000Z" ||
		sys.RetentionDays != 30 || !sys.HA.Connected || *sys.HA.Since != "2026-10-03T09:00:00.000Z" || *sys.HA.Version != "2026.9.4" ||
		*sys.HA.UserName != "Home-Mandate" || !slices.Contains(sys.HA.Commands, "config/auth/list") ||
		*sys.MCPURL != "https://hm.example.org:8765/mcp" || !sys.TLS.Present || *sys.TLS.ValidUntil != "2027-01-01T10:00:00.000Z" ||
		sys.EmergencyStop.Active || sys.EmergencyStop.Since != nil || !sys.Chain.Valid || sys.Chain.CheckedAt != nil ||
		sys.ApproversConfigured != 1 || sys.ClockBehind {
		t.Errorf("system = %+v", sys)
	}
	// A clock behind the newest audit entry is reported (SPEC-v0 section 11.4).
	if _, err := h.srv.cfg.Log.Append(t.Context(), audit.Entry{Event: audit.EventEmergencyStopReleased,
		Actor: &audit.Actor{Kind: audit.ActorUser, ID: adminID}}); err != nil {
		t.Fatal(err)
	}
	h.srv.cfg.Log.SetClock(func() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) })
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if !sys.ClockBehind {
		t.Error("clock_behind not reported")
	}
	h.srv.cfg.Log.SetClock(time.Now)
	if _, err := h.agents.SetEmergencyStop(t.Context(), true, audit.Actor{Kind: audit.ActorUser, ID: annaID}); err != nil {
		t.Fatal(err)
	}
	h.srv.cfg.TLS = func() TLSStatus { return TLSStatus{Present: true, ValidUntil: testStart, RenewalFailed: true} }
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if !sys.TLS.Present || !sys.TLS.RenewalFailed {
		t.Errorf("renewal failure not reported: %+v", sys.TLS)
	}
	h.srv.cfg.TLS = func() TLSStatus { return TLSStatus{Proxy: true} }
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.TLS.Present || !sys.TLS.Proxy || sys.TLS.ValidUntil != nil {
		t.Errorf("TLS at the reverse proxy not reported: %+v", sys.TLS)
	}
	h.srv.cfg.TLS = func() TLSStatus { return TLSStatus{} }
	h.srv.cfg.MCPURL = ""
	h.status.HAConnected, h.status.HAVersion = false, ""
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if !sys.EmergencyStop.Active || *sys.EmergencyStop.ByName != "Anna" || sys.EmergencyStop.Since == nil ||
		sys.TLS.Present || sys.TLS.ValidUntil != nil || sys.MCPURL != nil || sys.HA.Connected || sys.HA.Version != nil {
		t.Errorf("system = %+v", sys)
	}
	// By local-admin (command line): no name.
	_, _ = h.agents.SetEmergencyStop(t.Context(), false, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"})
	_, _ = h.agents.SetEmergencyStop(t.Context(), true, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"})
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.EmergencyStop.ByName != nil {
		t.Errorf("by_name of local-admin = %v", *sys.EmergencyStop.ByName)
	}
}

func TestVerify(t *testing.T) {
	h := newHarness(t)
	h.admit("Voice")
	var v wireVerification
	h.ok(http.MethodPost, "/api/audit/verify", nil, &v)
	if !v.Valid || v.Checked < 2 || v.CheckedAt == nil || v.BrokenAtSeq != nil {
		t.Errorf("verification = %+v", v)
	}
	if r := h.do(http.MethodPost, "/api/audit/verify", nil); r.errCode() != codeRateLimited || r.header.Get("Retry-After") != "10" {
		t.Errorf("second verification = %d %v", r.code, r.header)
	}
	// A tampered log is reported with its position, also in api/system.
	if _, err := h.st.DB().Exec(`UPDATE audit_log SET entry = replace(entry, 'Voice', 'Evil') WHERE seq = 1`); err != nil {
		t.Fatal(err)
	}
	h.now.Add(verifyPeriod)
	h.ok(http.MethodPost, "/api/audit/verify", nil, &v)
	if v.Valid || v.BrokenAtSeq == nil || *v.BrokenAtSeq < 1 {
		t.Errorf("tampered = %+v", v)
	}
	t.Logf("broken at %d", *v.BrokenAtSeq)
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.Chain.Valid || *sys.Chain.BrokenAtSeq != *v.BrokenAtSeq {
		t.Errorf("system chain = %+v", sys.Chain)
	}
}

// A deleted beginning that no verified checkpoint covers is reported as a broken chain
// at the first remaining entry: the log.truncated entry needs no key.
func TestVerifyReportsAnUnanchoredTruncation(t *testing.T) {
	h := newHarness(t)
	h.admit("Voice")
	h.admit("Lights")
	if _, err := h.log.Truncate(t.Context(), time.Now().AddDate(10, 0, 0), audit.Actor{Kind: audit.ActorSystem, ID: "retention"}); err != nil {
		t.Fatal(err)
	}
	var first int64
	_ = h.st.DB().QueryRow(`SELECT min(seq) FROM audit_log`).Scan(&first)
	var v wireVerification
	h.ok(http.MethodPost, "/api/audit/verify", nil, &v)
	if v.Valid || v.BrokenAtSeq == nil || *v.BrokenAtSeq != first {
		t.Errorf("verification = %+v, want broken at %d", v, first)
	}
}

func TestRunVerifierAndRunTailStop(t *testing.T) {
	h := newHarness(t)
	old := verifyEvery
	verifyEvery = 10 * time.Millisecond
	defer func() { verifyEvery = old }()
	ctx, cancel := context_(t)
	done := make(chan struct{}, 2)
	go func() { h.srv.RunVerifier(ctx); done <- struct{}{} }()
	go func() { h.srv.RunTail(ctx); done <- struct{}{} }()
	deadline := time.Now().Add(5 * time.Second)
	for h.srv.chain.get().CheckedAt == nil && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if h.srv.chain.get().CheckedAt == nil {
		t.Error("RunVerifier did not verify")
	}
	cancel()
	for range 2 {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("did not stop")
		}
	}
}

// logRecorder passes the messages of a slog.Logger on, dropping them when nobody reads.
type logRecorder struct{ messages chan string }

func (l logRecorder) Enabled(context.Context, slog.Level) bool { return true }
func (l logRecorder) WithAttrs([]slog.Attr) slog.Handler       { return l }
func (l logRecorder) WithGroup(string) slog.Handler            { return l }
func (l logRecorder) Handle(_ context.Context, r slog.Record) error {
	select {
	case l.messages <- r.Message:
	default:
	}
	return nil
}

func TestRunVerifierReportsWhenTheLogCannotBeRead(t *testing.T) {
	h := newHarness(t)
	rec := logRecorder{messages: make(chan string, 16)}
	h.srv.cfg.Logger = slog.New(rec)
	_ = h.st.Close()
	ctx, cancel := context_(t)
	done := make(chan struct{})
	go func() { h.srv.RunVerifier(ctx); close(done) }()
	for found := false; !found; {
		select {
		case msg := <-rec.messages:
			found = msg == "verifying the audit log failed"
		case <-time.After(5 * time.Second):
			t.Fatal("the failure was not reported")
		}
	}
	cancel()
	<-done
}

// failingRenames reports that storing renames fails.
type failingRenames struct {
	*catalog.Renames
	since    time.Time
	overflow bool
}

func (f failingRenames) FailingSince() time.Time { return f.since }
func (f failingRenames) Overflowing() bool       { return f.overflow }
func (f failingRenames) RenamesLastHour() int    { return 77 }

// Storing renames that fails for a while, and renames that cannot be held, are reported.
func TestSystemReportsRenamesThatCannotBeStored(t *testing.T) {
	h := newHarness(t)
	var sys wireSystem
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.Directory.StoreFailingSince != nil || sys.Directory.Overflow {
		t.Errorf("directory = %+v", sys.Directory)
	}
	// A short failure is no news.
	h.srv.cfg.Renames = failingRenames{Renames: h.renames, since: testStart.Add(-time.Minute)}
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.Directory.StoreFailingSince != nil {
		t.Errorf("reported after a minute: %+v", sys.Directory)
	}
	h.srv.cfg.Renames = failingRenames{Renames: h.renames, since: testStart.Add(-10 * time.Minute), overflow: true}
	h.ok(http.MethodGet, "/api/system", nil, &sys)
	if sys.Directory.StoreFailingSince == nil || *sys.Directory.StoreFailingSince != "2026-10-03T09:50:00.000Z" || !sys.Directory.Overflow ||
		sys.Directory.RenamesLastHour != 77 || sys.Directory.RenameFloodThreshold != 50 {
		t.Errorf("directory = %+v", sys.Directory)
	}
}
