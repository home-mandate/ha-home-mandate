// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// browserAgents are the agents web/e2e-live/oauth.spec.ts admits, in each language: one
// through the Authorization Code flow with PKCE, one through a pairing code. Its denial
// and its refused non-administrator admit nobody.
var browserAgents = []string{"Browser PKCE de", "Browser pairing de", "Browser PKCE en", "Browser pairing en"}

// The sign-in, consent and pairing pages of the authorization server in a real browser
// (Playwright, de and en), when asked for: make e2e-ui. The spec plays the agent too: it
// fetches the metadata, listens on loopback for the redirect, exchanges the code and
// calls the MCP endpoint. The client metadata comes from tools/cimdserver (env_test.go).
func TestOAuthInTheBrowser(t *testing.T) {
	requirePlaywright(t)
	ui := uiLogin(t, adminApprover)
	before := map[string]bool{}
	for _, a := range browserAgentList(ui) {
		before[a.ClientID] = true
	}
	// A hidden base template is not offered on the consent page.
	ui.ok(http.MethodPut, "api/templates/hm-light-climate/hidden", map[string]any{"hidden": true}, nil)
	t.Cleanup(func() {
		uiLogin(t, adminApprover).ok(http.MethodPut, "api/templates/hm-light-climate/hidden", map[string]any{"hidden": false}, nil)
	})

	playwright(t, "e2e-live/oauth.spec.ts", "HM_LIVE_PUBLIC="+env.public, "HM_LIVE_CLIENT_ID="+cimdClientID,
		"HM_LIVE_SPKI="+spkiHashes(t), "NODE_EXTRA_CA_CERTS="+filepath.Join(env.certs, "ca.pem"))

	// Exactly the approved agents were admitted, by admin-approver: not the denied one, not
	// the one the non-administrator tried to admit. The client of the code flow is the
	// checked metadata document, the pairing's a free identifier.
	var added []string
	for _, a := range browserAgentList(ui) {
		if before[a.ClientID] {
			continue
		}
		added = append(added, a.DisplayName)
		pkce := strings.HasPrefix(a.DisplayName, "Browser PKCE")
		if a.CreatedBy != env.users[adminApprover].id || a.Status != "active" || pkce != (a.OAuthClient == cimdClientID) ||
			pkce != a.ClientVerified || a.Mandate == nil {
			t.Errorf("agent admitted in the browser: %+v", a)
		}
	}
	slices.Sort(added)
	want := slices.Clone(browserAgents)
	slices.Sort(want)
	if !slices.Equal(added, want) {
		t.Errorf("admitted in the browser: %q, want %q", added, want)
	}
	log := auditLog(t)
	if !hasLine(log, `"event":"auth.rejected"`, `"error":"not_admin"`, `"id":"`+env.users[userPlain].id+`"`) {
		t.Error("the refused non-administrator is not in the audit log")
	}
	for _, name := range browserAgents {
		if !hasLine(log, `"event":"agent.registered"`, `"display_name":"`+name+`"`, `"id":"`+env.users[adminApprover].id+`"`) {
			t.Errorf("no audit entry of the admission of %q by admin-approver", name)
		}
	}
}

type browserAgent struct {
	ClientID       string `json:"client_id"`
	DisplayName    string `json:"display_name"`
	Status         string `json:"status"`
	CreatedBy      string `json:"created_by"`
	OAuthClient    string `json:"oauth_client"`
	ClientVerified bool   `json:"client_verified"`
	Mandate        *struct {
		Name string `json:"name"`
	} `json:"mandate"`
}

func browserAgentList(ui *uiClient) []browserAgent {
	var list []browserAgent
	ui.ok(http.MethodGet, "api/agents", nil, &list)
	return list
}

// spkiHashes are the SHA-256 of the public keys Home Assistant and the gateway present
// (a test may have renewed the gateway's certificate), for Chromium's
// --ignore-certificate-errors-spki-list: the browser accepts these keys of the test CA and
// nothing else.
func spkiHashes(t *testing.T) string {
	t.Helper()
	var hashes []string
	for _, origin := range []string{env.haURL, env.public} {
		addr := strings.TrimPrefix(origin, "https://")
		conn, err := tls.Dial("tcp", addr, &tls.Config{RootCAs: env.roots, ServerName: "localhost", MinVersion: tls.VersionTLS12})
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(conn.ConnectionState().PeerCertificates[0].RawSubjectPublicKeyInfo)
		conn.Close()
		hashes = append(hashes, base64.StdEncoding.EncodeToString(sum[:]))
	}
	return strings.Join(hashes, ",")
}
