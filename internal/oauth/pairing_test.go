// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	mandatespec "github.com/mandate-spec/mandate-spec"

	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
)

const uiSession = "8f2b1c0d9e7a4b3c8f2b1c0d9e7a4b3c" // a Home Assistant user ID

func approval(code, id string) PairingApproval {
	return PairingApproval{Code: code, PairingID: id, DisplayName: "Küche", Template: "voice-assistant", By: adminUser.ID}
}

func TestPairingInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	c, err := h.server.Check(ctx, uiSession, strings.ToLower(a.UserCode))
	if err != nil {
		t.Fatal(err)
	}
	if c.PairingID == "" || strings.Contains(c.PairingID, a.UserCode) || c.Client != "n8n-kitchen" || c.ClientVerified ||
		c.ClaimedName != "n8n-kitchen" || c.RequestedFrom != "127.0.0.1" || !c.ExpiresAt.Equal(c.RequestedAt.Add(deviceTTL)) {
		t.Errorf("candidate = %+v", c)
	}
	// The person looked at another request than the one behind the code now: conflict.
	if _, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, "other")); !errors.Is(err, ErrPairingConflict) {
		t.Errorf("Approve with another pairing ID = %v", err)
	}
	if n := countAgents(t, h); n != 0 {
		t.Fatalf("%d agents admitted on a conflict", n)
	}
	ap := approval(a.UserCode, c.PairingID)
	ap.MandateName = "Küchen-Mandat"
	got, err := h.server.Approve(ctx, uiSession, ap)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Küche" || got.OAuthClient != "n8n-kitchen" || got.CreatedBy != adminUser.ID {
		t.Errorf("agent = %+v", got)
	}
	// Decided: the code tells "expired", the agent gets its tokens once.
	if _, err := h.server.Check(ctx, uiSession, a.UserCode); !errors.Is(err, ErrPairingExpired) {
		t.Errorf("Check after approval = %v", err)
	}
	if _, err := h.server.Approve(ctx, uiSession, ap); !errors.Is(err, ErrPairingExpired) {
		t.Errorf("second Approve = %v", err)
	}
	h.clock.Add(deviceInterval)
	status, tokens := h.poll(a, "n8n-kitchen")
	if status != http.StatusOK || tokens["access_token"] == nil {
		t.Fatalf("poll = %d %v", status, tokens)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("second poll = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 1 {
		t.Errorf("%d agents", n)
	}
	if out := h.auditLog(); strings.Contains(out, a.UserCode) || strings.Contains(out, strings.ReplaceAll(a.UserCode, "-", "")) {
		t.Error("the pairing code is in the audit log")
	}
}

func TestPairingDenyInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	c, err := h.server.Check(ctx, uiSession, a.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.server.Deny(ctx, uiSession, a.UserCode, "other"); !errors.Is(err, ErrPairingConflict) {
		t.Errorf("Deny with another pairing ID = %v", err)
	}
	if err := h.server.Deny(ctx, uiSession, a.UserCode, c.PairingID); err != nil {
		t.Fatal(err)
	}
	if err := h.server.Deny(ctx, uiSession, a.UserCode, c.PairingID); !errors.Is(err, ErrPairingExpired) {
		t.Errorf("second Deny = %v", err)
	}
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusBadRequest || out["error"] != "access_denied" {
		t.Errorf("poll = %d %v", status, out)
	}
	if n := countAgents(t, h); n != 0 {
		t.Errorf("%d agents", n)
	}
}

// Negative catalog: two approvals at once → exactly one admits.
func TestPairingApprovalsRace(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	c, _ := h.server.Check(ctx, uiSession, a.UserCode)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() {
			_, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, c.PairingID))
			results <- err
		})
	}
	wg.Wait()
	close(results)
	ok := 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case !errors.Is(err, ErrPairingExpired):
			t.Errorf("losing approval = %v", err)
		}
	}
	if ok != 1 || countAgents(t, h) != 1 {
		t.Errorf("%d approvals succeeded, %d agents", ok, countAgents(t, h))
	}
}

// Negative catalog: a locked session or a global lock refuses a correct code too; the
// remaining time is real; the lock of one session does not lock another.
func TestPairingLocksInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	for i := range pairSessionMax {
		_, err := h.server.Check(ctx, uiSession, "BBBB-BBBB")
		var locked *PairingLockedError
		if i < pairSessionMax-1 && !errors.Is(err, ErrPairingInvalid) || i == pairSessionMax-1 && !errors.As(err, &locked) {
			t.Fatalf("attempt %d = %v", i+1, err)
		}
	}
	var locked *PairingLockedError
	h.clock.Add(time.Minute)
	if _, err := h.server.Check(ctx, uiSession, a.UserCode); !errors.As(err, &locked) || locked.RetryAfter != pairWindow-time.Minute {
		t.Errorf("right code in a locked session = %v", err)
	}
	if !strings.Contains(locked.Error(), "locked") {
		t.Errorf("error text %q", locked.Error())
	}
	if _, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, "x")); !errors.As(err, &locked) {
		t.Errorf("Approve in a locked session = %v", err)
	}
	if err := h.server.Deny(ctx, uiSession, a.UserCode, "x"); !errors.As(err, &locked) {
		t.Errorf("Deny in a locked session = %v", err)
	}
	if _, err := h.server.Check(ctx, "other-user", a.UserCode); err != nil {
		t.Errorf("another session = %v", err)
	}
	if out := h.auditLog(); strings.Count(out, `"error":"pairing_code_invalid"`) != pairSessionMax {
		t.Errorf("audit entries = %d", strings.Count(out, `"error":"pairing_code_invalid"`))
	}
	h.clock.Add(pairWindow)
	if _, err := h.server.Check(ctx, uiSession, "BBBB-BBBB"); !errors.Is(err, ErrPairingInvalid) {
		t.Errorf("after the window = %v", err)
	}
}

func TestPairingGlobalLockInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	for i := range pairGlobalMax {
		_, _ = h.server.Check(ctx, fmt.Sprint("user-", i), "not a code")
	}
	var locked *PairingLockedError
	if _, err := h.server.Check(ctx, "fresh-user", a.UserCode); !errors.As(err, &locked) || locked.RetryAfter != pairWindow {
		t.Errorf("fresh session during the global lock = %v", err)
	}
}

func TestPairingExpiredCodeInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	h.clock.Add(deviceTTL)
	if _, err := h.server.Check(ctx, uiSession, a.UserCode); !errors.Is(err, ErrPairingExpired) {
		t.Errorf("expired code = %v", err)
	}
	// An expired code is no wrong code: it does not count towards the lock.
	if out := h.auditLog(); strings.Contains(out, "pairing_code_invalid") {
		t.Error("expired code counted as wrong")
	}
}

// Negative catalog: a template whose rules allow critical actions without approval needs
// the separate confirmation also when pairing; a refused admission leaves the pairing open.
func TestPairingWithACriticalTemplate(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	doc["rules"] = []any{map[string]any{"id": "r-unlock", "resource": map[string]any{"category": "lock"}, "actions": []any{"unlock"},
		"decision": "allow", "allow_critical": true}}
	critical, _ := json.Marshal(doc)
	if err := h.adm.PutTemplate(ctx, "doors", critical, audit.Actor{Kind: audit.ActorUser, ID: "local-admin"}); err != nil {
		t.Fatal(err)
	}
	a := h.device("n8n-kitchen")
	c, _ := h.server.Check(ctx, uiSession, a.UserCode)
	ap := approval(a.UserCode, c.PairingID)
	ap.Template = "doors"
	if _, err := h.server.Approve(ctx, uiSession, ap); !errors.Is(err, ErrPairingAdmission) || !errors.Is(err, mandate.ErrCriticalConfirmation) {
		t.Fatalf("without confirmation = %v", err)
	}
	if n := countAgents(t, h); n != 0 {
		t.Fatalf("%d agents", n)
	}
	ap.ConfirmCritical = true
	if _, err := h.server.Approve(ctx, uiSession, ap); err != nil {
		t.Errorf("with confirmation = %v", err)
	}
}

func TestPairingServerErrorInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	c, _ := h.server.Check(ctx, uiSession, a.UserCode)
	h.server.cfg.Admission = brokenAdmission{Admitter: h.adm, admitErr: errors.New("database is locked")}
	if _, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, c.PairingID)); !errors.Is(err, ErrPairingUnavailable) {
		t.Errorf("Approve with a broken database = %v", err)
	}
	h.server.cfg.Admission = h.adm
	if _, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, c.PairingID)); err != nil {
		t.Errorf("Approve after recovery = %v", err)
	}
}

func TestNormalizeAddr(t *testing.T) {
	for in, want := range map[string]string{
		"192.168.1.42":     "192.168.1.42",
		"::ffff:10.0.0.1":  "10.0.0.1",
		"fe80::1%eth0":     "fe80::1",
		"2001:DB8::1":      "2001:db8::1",
		"not an address":   "",
		"":                 "",
		"192.168.1.42:80":  "",
		"<script>":         "",
		"10.0.0.1\r\nX: y": "",
	} {
		if got := normalizeAddr(in); got != want {
			t.Errorf("normalizeAddr(%q) = %q, want %q", in, got, want)
		}
	}
}

// Many UI sessions with wrong codes: the store forgets old ones instead of growing.
func TestUIFailuresAreBounded(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	for i := range maxUISessions {
		_, _ = h.server.Check(ctx, fmt.Sprint("s", i), "BBBB-BBBB")
		if i%(pairGlobalMax-1) == pairGlobalMax-2 {
			h.clock.Add(pairWindow) // keep clear of the global lock
		}
	}
	h.clock.Add(pairWindow)
	_, _ = h.server.Check(ctx, "one-more", "BBBB-BBBB")
	h.server.mu.Lock()
	n := len(h.server.uiFailures)
	h.server.mu.Unlock()
	if n > maxUISessions {
		t.Errorf("%d sessions kept", n)
	}
}

// Review L2: parallel wrong codes of one session cannot all pass the lock check before
// the fifth is counted.
func TestParallelWrongCodesAreCountedInOrder(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan error, 20)
	for range 20 {
		wg.Go(func() {
			_, err := h.server.Check(ctx, uiSession, "BBBB-BBBB")
			results <- err
		})
	}
	wg.Wait()
	close(results)
	invalid := 0
	for err := range results {
		if errors.Is(err, ErrPairingInvalid) {
			invalid++
		}
	}
	if invalid != pairSessionMax-1 {
		t.Errorf("%d wrong codes answered as invalid, want %d (the rest locked)", invalid, pairSessionMax-1)
	}
	if out := h.auditLog(); strings.Count(out, `"error":"pairing_code_invalid"`) != pairSessionMax {
		t.Errorf("counted %d", strings.Count(out, `"error":"pairing_code_invalid"`))
	}
}

// Review L4: an approval shortly before the code expires keeps the tokens for the
// agent's poll for issuedGrace.
func TestIssuedTokensOutliveTheCode(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.device("n8n-kitchen")
	c, _ := h.server.Check(ctx, uiSession, a.UserCode)
	h.clock.Add(deviceTTL - time.Second)
	if _, err := h.server.Approve(ctx, uiSession, approval(a.UserCode, c.PairingID)); err != nil {
		t.Fatal(err)
	}
	h.clock.Add(issuedGrace / 2)
	h.device("n8n-kitchen") // sweeps expired grants
	if status, out := h.poll(a, "n8n-kitchen"); status != http.StatusOK {
		t.Errorf("poll after the code expired = %d %v", status, out)
	}
}
