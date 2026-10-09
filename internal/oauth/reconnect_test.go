// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// These tests write agent.reconnected: they need a specification with that event
// (home-mandate/spec v0.1.0-alpha.3).

// stopAndRelease switches the emergency stop on and off: every token is withdrawn.
func (h *harness) stopAndRelease() {
	h.t.Helper()
	for _, on := range []bool{true, false} {
		if _, err := h.agents.SetEmergencyStop(context.Background(), on, audit.Actor{Kind: audit.ActorUser, ID: "u-admin"}); err != nil {
			h.t.Fatal(err)
		}
	}
}

// admitByBrowser admits an agent through the browser sign-in and returns it.
func (h *harness) admitByBrowser() agent.Agent {
	h.t.Helper()
	b := h.browser()
	verifier, challenge := pkce()
	code := b.approve(b.consentAs(challenge, "admin-code"))
	status, tokens, _ := h.tokenRequest(exchangeForm(code, verifier))
	if status != http.StatusOK {
		h.t.Fatalf("token = %d %v", status, tokens)
	}
	a, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource)
	if err != nil {
		h.t.Fatal(err)
	}
	return a
}

func TestConsentOffersToReconnectAfterAnEmergencyStop(t *testing.T) {
	h := newHarness(t)
	a := h.admitByBrowser()
	b := h.browser()
	_, challenge := pkce()
	if page := b.consentAs(challenge, "admin-code"); strings.Contains(page.body, `value="reconnect"`) {
		t.Errorf("reconnect offered while the agent has tokens:\n%s", page.body)
	}

	h.stopAndRelease()
	b = h.browser()
	verifier, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	if page.status != http.StatusOK || !strings.Contains(page.body, `name="agent" value="`+a.ClientID+`"`) ||
		!strings.Contains(page.body, "Reconnect an existing agent") || !strings.Contains(page.body, "Admit as a new agent") {
		t.Fatalf("consent page:\n%s", page.body)
	}
	if strings.Contains(page.body, "the name the agent gave itself") {
		t.Error("warning about a self-chosen name for a verified client")
	}
	// Nothing is chosen for the human: the radio buttons start unchecked.
	if strings.Contains(page.body, `value="`+a.ClientID+`" required checked`) {
		t.Error("an agent to reconnect is preselected")
	}
	csrf := csrfOf(t, page.body)
	// Only an offered agent can be chosen.
	bad := b.post(ConsentPath, url.Values{"csrf": {csrf}, "action": {"reconnect"}, "agent": {"hm-client:other-00000000"}})
	if bad.status != http.StatusBadRequest || !strings.Contains(bad.body, "Choose one of the agents") {
		t.Fatalf("reconnect of another agent = %d\n%s", bad.status, bad.body)
	}
	res := b.post(ConsentPath, url.Values{"csrf": {csrf}, "action": {"reconnect"}, "agent": {a.ClientID}})
	if res.status != http.StatusSeeOther || !strings.HasPrefix(res.location, testRedirect+"?") {
		t.Fatalf("reconnect = %d %q\n%s", res.status, res.location, res.body)
	}
	u, _ := url.Parse(res.location)
	status, tokens, _ := h.tokenRequest(exchangeForm(u.Query().Get("code"), verifier))
	if status != http.StatusOK {
		t.Fatalf("token = %d %v", status, tokens)
	}
	got, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource)
	if err != nil || got.ClientID != a.ClientID {
		t.Errorf("reconnected agent = %+v, %v", got, err)
	}
	if n := countAgents(t, h); n != 1 {
		t.Errorf("%d agents", n)
	}
	out := h.auditLog()
	if !strings.Contains(out, `"actor":{"id":"u-admin","kind":"user"},"agent":{"client_id":"`+a.ClientID+`","display_name":"Claude"},"event":"agent.reconnected"`) {
		t.Errorf("audit log:\n%s", out)
	}
}

// The code of a reconnection is refused once the agent got tokens another way.
func TestReconnectRaceIsRefused(t *testing.T) {
	h := newHarness(t)
	a := h.admitByBrowser()
	h.stopAndRelease()
	b := h.browser()
	verifier, challenge := pkce()
	page := b.consentAs(challenge, "admin-code")
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, page.body)}, "action": {"reconnect"}, "agent": {a.ClientID}})
	u, _ := url.Parse(res.location)
	if _, err := h.agents.IssueTokens(context.Background(), a.ClientID, testResource); err != nil {
		t.Fatal(err)
	}
	if status, out, _ := h.tokenRequest(exchangeForm(u.Query().Get("code"), verifier)); status != http.StatusBadRequest || out["error"] != "invalid_grant" {
		t.Errorf("token = %d %v", status, out)
	}
}

func TestPairingPageOffersToReconnect(t *testing.T) {
	h := newHarness(t)
	first := h.device("n8n-kitchen")
	b, page := h.pairBrowser("admin-code")
	consent := b.get(b.enterCode(page, first.UserCode).location)
	if res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"approve"}, "name": {"Kitchen n8n"},
		"template": {voiceChoice}}); res.status != http.StatusOK {
		t.Fatalf("approve = %d\n%s", res.status, res.body)
	}
	h.clock.Add(deviceInterval)
	if status, _ := h.poll(first, "n8n-kitchen"); status != http.StatusOK {
		t.Fatal("no tokens")
	}
	admitted, _ := h.agents.List(context.Background())

	h.stopAndRelease()
	again := h.device("n8n-kitchen")
	b, page = h.pairBrowser("admin-code")
	consent = b.get(b.enterCode(page, again.UserCode).location)
	if !strings.Contains(consent.body, `name="agent" value="`+admitted[0].ClientID+`"`) || !strings.Contains(consent.body, "Kitchen n8n") {
		t.Fatalf("consent:\n%s", consent.body)
	}
	// A pairing code's client ID is only the name the agent gave itself: the page says so and
	// names the address the request came from.
	if !strings.Contains(consent.body, "the name the agent gave itself") || !strings.Contains(consent.body, "127.0.0.1") {
		t.Errorf("no warning for an unverified client:\n%s", consent.body)
	}
	res := b.post(ConsentPath, url.Values{"csrf": {csrfOf(t, consent.body)}, "action": {"reconnect"}, "agent": {admitted[0].ClientID}})
	if res.status != http.StatusOK || !strings.Contains(res.body, "reconnected") {
		t.Fatalf("reconnect = %d\n%s", res.status, res.body)
	}
	h.clock.Add(deviceInterval)
	status, tokens := h.poll(again, "n8n-kitchen")
	if status != http.StatusOK {
		t.Fatalf("poll = %d %v", status, tokens)
	}
	if got, err := h.agents.Authenticate(context.Background(), tokens["access_token"].(string), testResource); err != nil || got.ClientID != admitted[0].ClientID {
		t.Errorf("agent = %+v, %v", got, err)
	}
	if n := countAgents(t, h); n != 1 {
		t.Errorf("%d agents", n)
	}
}

func TestReconnectInTheUI(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	first := h.device("n8n-kitchen")
	c, err := h.server.Check(ctx, uiSession, first.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Reconnect) != 0 {
		t.Errorf("reconnect offered without agents: %+v", c.Reconnect)
	}
	admitted, err := h.server.Approve(ctx, uiSession, approval(first.UserCode, c.PairingID))
	if err != nil {
		t.Fatal(err)
	}
	h.clock.Add(deviceInterval)
	h.poll(first, "n8n-kitchen")

	h.stopAndRelease()
	again := h.device("n8n-kitchen")
	c, err = h.server.Check(ctx, uiSession, again.UserCode)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Reconnect) != 1 || c.Reconnect[0].ClientID != admitted.ClientID || c.Reconnect[0].DisplayName != "Küche" ||
		c.Reconnect[0].MandateName != "Küche" || c.Reconnect[0].MandateID == "" || c.Reconnect[0].AdmittedAt.IsZero() {
		t.Fatalf("reconnect = %+v", c.Reconnect)
	}
	r := PairingReconnect{Code: again.UserCode, PairingID: c.PairingID, ClientID: "hm-client:other-00000000", By: adminUser.ID}
	if _, err := h.server.Reconnect(ctx, uiSession, r); !errors.Is(err, ErrPairingAdmission) {
		t.Errorf("reconnect of an agent not offered = %v", err)
	}
	r.PairingID = "other"
	if _, err := h.server.Reconnect(ctx, uiSession, r); !errors.Is(err, ErrPairingConflict) {
		t.Errorf("reconnect for another request = %v", err)
	}
	r.PairingID, r.ClientID = c.PairingID, admitted.ClientID
	got, err := h.server.Reconnect(ctx, uiSession, r)
	if err != nil || got.ClientID != admitted.ClientID {
		t.Fatalf("Reconnect = %+v, %v", got, err)
	}
	if _, err := h.server.Reconnect(ctx, uiSession, r); !errors.Is(err, ErrPairingExpired) {
		t.Errorf("second Reconnect = %v", err)
	}
	h.clock.Add(deviceInterval)
	status, tokens := h.poll(again, "n8n-kitchen")
	if status != http.StatusOK {
		t.Fatalf("poll = %d %v", status, tokens)
	}
	if who, err := h.agents.Authenticate(ctx, tokens["access_token"].(string), testResource); err != nil || who.ClientID != admitted.ClientID {
		t.Errorf("agent = %+v, %v", who, err)
	}
}
