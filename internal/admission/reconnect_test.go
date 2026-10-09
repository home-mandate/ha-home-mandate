// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
)

// These tests write agent.reconnected: they need a specification with that event
// (home-mandate/spec v0.1.0-alpha.3).

// After an emergency stop, an administrator reconnects the agent to its entry: same
// client ID, same (customised) mandate, new tokens.
func TestReconnectAfterAnEmergencyStop(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	req := request()
	req.MandateName = "Kitchen rules"
	a, old, err := e.adm.Admit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	cimd := agent.Client{ID: client, Verified: true}
	if list, err := e.adm.Reconnectable(ctx, cimd); err != nil || len(list) != 0 {
		t.Fatalf("before the stop = %+v, %v", list, err)
	}
	for _, on := range []bool{true, false} {
		if _, err := e.agents.SetEmergencyStop(ctx, on, admin); err != nil {
			t.Fatal(err)
		}
	}
	list, err := e.adm.Reconnectable(ctx, cimd)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Agent.ClientID != a.ClientID || list[0].MandateName != "Kitchen rules" || list[0].MandateID == "" {
		t.Fatalf("reconnectable = %+v", list)
	}
	if list, _ := e.adm.Reconnectable(ctx, agent.Client{ID: client}); len(list) != 0 {
		t.Errorf("an unverified client with the same ID is offered %+v", list)
	}
	r := admission.ReconnectRequest{ClientID: a.ClientID, OAuthClient: client, ClientVerified: true, Resource: resource, By: admin}
	for name, bad := range map[string]admission.ReconnectRequest{
		"no actor":      {ClientID: a.ClientID, OAuthClient: client, ClientVerified: true, Resource: resource},
		"the system":    {ClientID: a.ClientID, OAuthClient: client, ClientVerified: true, Resource: resource, By: audit.Actor{Kind: audit.ActorSystem, ID: "x"}},
		"other client":  {ClientID: a.ClientID, OAuthClient: "https://other.example/c.json", ClientVerified: true, Resource: resource, By: admin},
		"unverified":    {ClientID: a.ClientID, OAuthClient: client, Resource: resource, By: admin},
		"unknown agent": {ClientID: "hm-client:none-00000000", OAuthClient: client, ClientVerified: true, Resource: resource, By: admin},
	} {
		if _, _, err := e.adm.Reconnect(ctx, bad); err == nil {
			t.Errorf("%s: reconnected", name)
		}
	}
	got, tokens, err := e.adm.Reconnect(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if got.ClientID != a.ClientID || tokens.AccessToken == "" {
		t.Errorf("reconnected = %+v", got)
	}
	if who, err := e.agents.Authenticate(ctx, tokens.AccessToken, resource); err != nil || who.ClientID != a.ClientID {
		t.Errorf("new token = %+v, %v", who, err)
	}
	if _, err := e.agents.Authenticate(ctx, old.AccessToken, resource); !errors.Is(err, agent.ErrUnauthorized) {
		t.Errorf("withdrawn token = %v", err)
	}
	// A second reconnect is refused: the agent has tokens again.
	if _, _, err := e.adm.Reconnect(ctx, r); !errors.Is(err, agent.ErrNotReconnectable) {
		t.Errorf("second reconnect = %v", err)
	}
	if n := count(t, e.db, "agents"); n != 1 {
		t.Errorf("%d agents", n)
	}
	if n := count(t, e.db, "mandates"); n != 1 {
		t.Errorf("%d mandates", n)
	}
	var buf strings.Builder
	if err := e.log.Export(ctx, &buf); err != nil {
		t.Fatal(err)
	}
	if strings.Count(buf.String(), `"event":"agent.reconnected"`) != 1 {
		t.Errorf("audit log:\n%s", buf.String())
	}
}

func TestReconnectableWithoutAnActiveMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	a, _, err := e.adm.Admit(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.agents.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.agents.SetEmergencyStop(ctx, false, admin); err != nil {
		t.Fatal(err)
	}
	m, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.mandates.Revoke(ctx, m.Info.ID, admin); err != nil {
		t.Fatal(err)
	}
	list, err := e.adm.Reconnectable(ctx, agent.Client{ID: client, Verified: true})
	if err != nil || len(list) != 1 || list[0].MandateID != "" || list[0].MandateName != "" {
		t.Errorf("reconnectable = %+v, %v", list, err)
	}
}
