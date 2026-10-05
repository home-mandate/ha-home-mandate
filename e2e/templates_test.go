// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// A new installation admits an agent with a base template, without importing anything:
// the approvers placeholder becomes the human who admits it (ARCHITECTURE section 6).
func TestAdmissionWithABaseTemplate(t *testing.T) {
	admit(t, "Basis", "hm-voice-cautious")
	clientID := clientIDOf(t, "Basis")
	ui := uiLogin(t, adminApprover)
	var list []struct {
		ID       string `json:"id"`
		ClientID string `json:"client_id"`
	}
	ui.ok(http.MethodGet, "api/mandates", nil, &list)
	id := ""
	for _, m := range list {
		if m.ClientID == clientID {
			id = m.ID
		}
	}
	if id == "" {
		t.Fatalf("no mandate for %s in %+v", clientID, list)
	}
	status, body := ui.do(http.MethodGet, "api/mandates/"+id, nil)
	if status != http.StatusOK || strings.Contains(string(body), "$approvers") || !strings.Contains(string(body), env.users[adminApprover].id) {
		t.Errorf("mandate from a base template = %d %s", status, body)
	}

	// Base templates are listed, cannot be changed, and a hidden one admits nobody.
	var templates []struct {
		Name    string `json:"name"`
		Builtin bool   `json:"builtin"`
	}
	ui.ok(http.MethodGet, "api/templates", nil, &templates)
	if len(templates) < 3 || templates[0].Name != "hm-read-only" || !templates[0].Builtin {
		t.Errorf("templates = %+v", templates)
	}
	if status, body := ui.do(http.MethodDelete, "api/templates/hm-read-only", nil); status != http.StatusConflict ||
		!strings.Contains(string(body), "builtin_template") {
		t.Errorf("removing a base template = %d %s", status, body)
	}
	ui.ok(http.MethodPut, "api/templates/hm-light-climate/hidden", map[string]any{"hidden": true}, nil)
	defer ui.ok(http.MethodPut, "api/templates/hm-light-climate/hidden", map[string]any{"hidden": false}, nil)
	p := requestPairing(t)
	res := pairAs(t, p, adminApprover, "Versteckt", "hm-light-climate")
	if res.status != http.StatusBadRequest || strings.Contains(res.body, `value="hm-light-climate"`) {
		t.Errorf("admission with a hidden template = %d", res.status)
	}
}
