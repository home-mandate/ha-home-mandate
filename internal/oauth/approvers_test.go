// SPDX-License-Identifier: AGPL-3.0-or-later

package oauth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	reach "github.com/home-mandate/ha-home-mandate/internal/approval"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// fakePreview answers who may approve, per template.
type fakePreview struct {
	mu       sync.Mutex
	previews map[string]reach.ReachPreview
	err      error
	asked    []audit.Actor
}

func (f *fakePreview) ApproversPreview(_ context.Context, template string, by audit.Actor) (reach.ReachPreview, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, by)
	if f.err != nil {
		return reach.ReachPreview{}, f.err
	}
	p, ok := f.previews[template]
	if !ok {
		return reach.ReachPreview{Normal: reach.CoverageNotNeeded, Critical: reach.CoverageNotNeeded}, nil
	}
	return p, nil
}

// detailsOf returns the part of the consent page that describes one template.
func detailsOf(t *testing.T, body, template string) string {
	t.Helper()
	start := strings.Index(body, `value="`+template+`@`)
	if start < 0 {
		t.Fatalf("template %s not offered:\n%s", template, body)
	}
	rest := body[start:]
	if end := strings.Index(rest[1:], `<input type="radio"`); end >= 0 {
		rest = rest[:end+1]
	}
	return rest
}

func (b *browser) consentIn(lang string) response {
	req, _ := http.NewRequest(http.MethodGet, b.h.srv.URL+ConsentPath, nil)
	req.Header.Set("Accept-Language", lang)
	return b.do(req)
}

func TestConsentShowsWhoMayApprove(t *testing.T) {
	h := newHarness(t)
	f := &fakePreview{previews: map[string]reach.ReachPreview{
		"hm-voice-cautious": {People: []reach.PersonReach{
			{UserID: adminUser.ID, Name: "Markus", Normal: reach.ReachNone, Critical: reach.ReachNone},
			{UserID: "u-anna", Name: `<b onclick="x()">Anna</b>`, Normal: reach.ReachPush, Critical: reach.ReachNone},
			{UserID: "u-ui", Name: "Uwe", Normal: reach.ReachUI, Critical: reach.ReachUI},
			{UserID: "u-gone", Normal: reach.ReachNone, Critical: reach.ReachNone},
			{UserID: "u-hm", Name: "Home-Mandate", Normal: reach.ReachNone, Critical: reach.ReachNone, Service: true},
		}, Normal: reach.CoverageNotNeeded, Critical: reach.CoverageNobody},
		"hm-light-climate": {People: []reach.PersonReach{
			{UserID: adminUser.ID, Name: "Markus", Normal: reach.ReachPush, Critical: reach.ReachPush},
		}, Normal: reach.CoverageNotNeeded, Critical: reach.CoverageReachable},
	}}
	h.server.SetApprovers(f)
	_, challenge := pkce()
	b := h.browser()
	page := b.consentAs(challenge, "admin-code")
	if page.status != http.StatusOK {
		t.Fatalf("consent page = %d", page.status)
	}
	voice := detailsOf(t, page.body, "hm-voice-cautious")
	for _, want := range []string{"Who may approve", "Markus (you)", "no channel for approval requests",
		"&lt;b onclick=&#34;x()&#34;&gt;Anna&lt;/b&gt;", "not for critical actions", "Uwe", "only in the Home-Mandate UI",
		"Unknown person", "Home-Mandate’s own user, never asked",
		"Nobody can answer approval requests for critical actions of this mandate yet"} {
		if !strings.Contains(voice, want) {
			t.Errorf("voice template lacks %q:\n%s", want, voice)
		}
	}
	if strings.Contains(page.body, "<b onclick") {
		t.Error("a name from Home Assistant is markup")
	}
	if strings.Contains(voice, "Nobody can answer approval requests of this mandate yet") {
		t.Error("ordinary requests warned although the template asks for none")
	}
	light := detailsOf(t, page.body, "hm-light-climate")
	if !strings.Contains(light, "Markus (you)") || strings.Contains(light, "Nobody can answer") || strings.Contains(light, "no channel") {
		t.Errorf("light template:\n%s", light)
	}
	for _, by := range f.asked {
		if by != (audit.Actor{Kind: audit.ActorUser, ID: adminUser.ID}) {
			t.Errorf("asked for %+v", by)
		}
	}

	de := b.consentIn("de")
	voice = detailsOf(t, de.body, "hm-voice-cautious")
	for _, want := range []string{"Wer Rückfragen beantworten darf", "Markus (du)", "kein Weg für Rückfragen eingerichtet",
		"&lt;b onclick=&#34;x()&#34;&gt;Anna&lt;/b&gt;", "Niemand kann Rückfragen zu kritischen Aktionen"} {
		if !strings.Contains(voice, want) {
			t.Errorf("German page lacks %q:\n%s", want, voice)
		}
	}
	// The warning does not block: the human can still admit.
	code := b.approve(page)
	if code == "" {
		t.Error("no code")
	}
}

func TestConsentWarnsWhenNobodyAnswersOrdinaryRequests(t *testing.T) {
	h := newHarness(t)
	h.server.SetApprovers(&fakePreview{previews: map[string]reach.ReachPreview{
		"voice-assistant": {People: []reach.PersonReach{{UserID: "user-1", Normal: reach.ReachNone, Critical: reach.ReachNone}},
			Normal: reach.CoverageNobody, Critical: reach.CoverageNobody},
	}})
	_, challenge := pkce()
	page := h.browser().consentAs(challenge, "admin-code")
	voice := detailsOf(t, page.body, "voice-assistant")
	for _, want := range []string{"Nobody can answer approval requests of this mandate yet", "set up an approver first",
		"Nobody can answer approval requests for critical actions", `class="warn"`} {
		if !strings.Contains(voice, want) {
			t.Errorf("lacks %q:\n%s", want, voice)
		}
	}
}

// Home Assistant not asked, or the preview failing: unknown, never reachable, and the
// page still works.
func TestConsentShowsUnknownReach(t *testing.T) {
	cases := map[string]*fakePreview{
		"Home Assistant not asked": {previews: map[string]reach.ReachPreview{
			"hm-voice-cautious": {People: []reach.PersonReach{{UserID: adminUser.ID, Normal: reach.ReachUnknown, Critical: reach.ReachUnknown}},
				Normal: reach.CoverageNotNeeded, Critical: reach.CoverageUnknown},
		}},
		"preview failed": {err: errors.New("database down")},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.server.SetApprovers(f)
			_, challenge := pkce()
			page := h.browser().consentAs(challenge, "admin-code")
			if page.status != http.StatusOK {
				t.Fatalf("consent page = %d", page.status)
			}
			voice := detailsOf(t, page.body, "hm-voice-cautious")
			if !strings.Contains(voice, "Could not check whether anyone can answer approval requests") {
				t.Errorf("no unknown notice:\n%s", voice)
			}
			if strings.Contains(voice, "Nobody can answer") {
				t.Errorf("unknown shown as nobody:\n%s", voice)
			}
		})
	}
}

func TestConsentWithoutPreviewShowsNoApprovers(t *testing.T) {
	h := newHarness(t)
	_, challenge := pkce()
	page := h.browser().consentAs(challenge, "admin-code")
	if strings.Contains(page.body, "Who may approve") || strings.Contains(page.body, "Could not check") {
		t.Errorf("approvers shown without a preview:\n%s", page.body)
	}
}
