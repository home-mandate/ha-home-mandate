// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mandatespec "github.com/mandate-spec/mandate-spec"

	"github.com/home-mandate/home-mandate/internal/admission"
	"github.com/home-mandate/home-mandate/internal/agent"
	"github.com/home-mandate/home-mandate/internal/audit"
	"github.com/home-mandate/home-mandate/internal/mandate"
	"github.com/home-mandate/home-mandate/internal/store"
)

const (
	household = "household:hm-0123456789ab"
	resource  = "https://hm.example.org/mcp"
	client    = "https://claude.example.org/client.json"
)

var (
	admin = audit.Actor{Kind: audit.ActorUser, ID: "8f2b1c0d9e7a4b3c8f2b1c0d9e7a4b3c"}
	now   = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
)

type env struct {
	adm      *admission.Store
	agents   *agent.Store
	mandates *mandate.Store
	log      *audit.Log
	db       *sql.DB
}

func newEnv(t *testing.T) env {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "hm.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	log := audit.New(st.DB(), household)
	agents := agent.New(st.DB(), log)
	mandates := mandate.New(st.DB(), log, household)
	adm := admission.New(st.DB(), agents, mandates, household)
	adm.SetClock(func() time.Time { return now })
	return env{adm: adm, agents: agents, mandates: mandates, log: log, db: st.DB()}
}

// template is the voice assistant example of mandate-spec, edited by edit.
func template(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := fs.ReadFile(mandatespec.FS(), "examples/voice-assistant.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	_ = json.Unmarshal(data, &doc)
	if edit != nil {
		edit(doc)
	}
	out, _ := json.Marshal(doc)
	return out
}

func request() admission.Request {
	return admission.Request{DisplayName: "Claude", Template: "voice-assistant", OAuthClient: client, ClientVerified: true,
		Resource: resource, By: admin}
}

func count(t *testing.T, db *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTemplates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.PutTemplate(ctx, "lights-only", template(t, func(d map[string]any) {
		d["rules"] = []any{map[string]any{"id": "r-lights", "resource": map[string]any{"category": "light"}, "actions": []any{"*"}, "decision": "allow"}}
	}), admin); err != nil {
		t.Fatal(err)
	}
	list, err := e.adm.Templates(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "lights-only" || list[1].Name != "voice-assistant" || list[1].CreatedBy != admin.ID {
		t.Errorf("templates = %+v, %v", list, err)
	}
	// Replacing keeps one entry.
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.RemoveTemplate(ctx, "lights-only"); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.RemoveTemplate(ctx, "lights-only"); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("second remove = %v", err)
	}
	if list, _ := e.adm.Templates(ctx); len(list) != 1 {
		t.Errorf("templates = %+v", list)
	}
}

func TestPutTemplateRejects(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for name, tc := range map[string]struct {
		name string
		doc  []byte
	}{
		"name with space":    {"voice assistant", template(t, nil)},
		"name upper case":    {"Voice", template(t, nil)},
		"empty name":         {"", template(t, nil)},
		"long name":          {strings.Repeat("a", 33), template(t, nil)},
		"not json":           {"x", []byte(`{`)},
		"not an object":      {"x", []byte(`[]`)},
		"action not in cat.": {"x", template(t, func(d map[string]any) { d["rules"].([]any)[1].(map[string]any)["actions"] = []any{"unlock"} })},
		"default allow":      {"x", template(t, func(d map[string]any) { d["default"] = "allow" })},
		"unknown field":      {"x", template(t, func(d map[string]any) { d["extra"] = true })},
		"oversized":          {"x", append(template(t, nil)[:1], append(bytes.Repeat([]byte(" "), 300<<10), template(t, nil)[1:]...)...)},
	} {
		if err := e.adm.PutTemplate(ctx, tc.name, tc.doc, admin); !errors.Is(err, admission.ErrInvalidTemplate) {
			t.Errorf("%s: %v, want ErrInvalidTemplate", name, err)
		}
	}
	if n := count(t, e.db, "mandate_templates"); n != 0 {
		t.Errorf("%d templates stored", n)
	}
}

func TestAdmit(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, func(d map[string]any) {
		d["expires"] = "2026-10-20T00:00:00+02:00" // a template never carries an absolute expiry over
	}), admin); err != nil {
		t.Fatal(err)
	}
	a, tokens, err := e.adm.Admit(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	if a.DisplayName != "Claude" || a.OAuthClient != client || !a.ClientVerified || a.CreatedBy != admin.ID {
		t.Errorf("agent = %+v", a)
	}
	if got, err := e.agents.Authenticate(ctx, tokens.AccessToken, resource); err != nil || got.ClientID != a.ClientID {
		t.Errorf("Authenticate = %+v, %v", got, err)
	}
	loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Info.ID != "m-"+strings.TrimPrefix(a.ClientID, "hm-client:") || loaded.Info.MaxActionsPerHour != 60 {
		t.Errorf("mandate = %+v", loaded.Info)
	}
	versions, _ := e.mandates.Versions(ctx, loaded.Info.ID)
	if len(versions) != 1 || versions[0].CreatedBy != admin.ID {
		t.Errorf("versions = %+v", versions)
	}
	var doc map[string]any
	var raw string
	_ = e.db.QueryRow(`SELECT document FROM mandate_versions`).Scan(&raw)
	_ = json.Unmarshal([]byte(raw), &doc)
	agentDoc, _ := doc["agent"].(map[string]any)
	if doc["principal"] != household || agentDoc["client_id"] != a.ClientID || agentDoc["display_name"] != "Claude" ||
		doc["created_by"] != admin.ID || doc["valid_from"] != "2026-10-13T12:00:00Z" || doc["created_at"] != "2026-10-13T12:00:00Z" ||
		doc["expires"] != nil {
		t.Errorf("mandate document = %s", raw)
	}
	var buf bytes.Buffer
	_ = e.log.Export(ctx, &buf)
	out := buf.String()
	if !strings.Contains(out, `"event":"agent.registered"`) || !strings.Contains(out, `"event":"mandate.created"`) ||
		strings.Count(out, `"id":"`+admin.ID+`"`) != 2 {
		t.Errorf("audit log:\n%s", out)
	}
}

// Admission is one transaction: if any part fails, no agent, mandate or token remains.
func TestAdmitIsAllOrNothing(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		edit func(*admission.Request)
		want error
	}{
		"unknown template": {func(r *admission.Request) { r.Template = "none" }, admission.ErrTemplateNotFound},
		"bad name":         {func(r *admission.Request) { r.DisplayName = "bad\u202ename" }, agent.ErrInvalidName},
		"no resource":      {func(r *admission.Request) { r.Resource = "" }, nil},
		"no actor":         {func(r *admission.Request) { r.By = audit.Actor{} }, nil},
	} {
		req := request()
		tc.edit(&req)
		if _, _, err := e.adm.Admit(ctx, req); err == nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}
	if _, err := e.agents.SetEmergencyStop(ctx, true, admin); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.adm.Admit(ctx, request()); !errors.Is(err, agent.ErrEmergencyStop) {
		t.Errorf("during emergency stop: %v", err)
	}
	for _, table := range []string{"agents", "mandates", "tokens"} {
		if n := count(t, e.db, table); n != 0 {
			t.Errorf("%d rows left in %s", n, table)
		}
	}
	if r, err := e.log.Verify(ctx); err != nil || !r.Valid {
		t.Errorf("audit log = %+v, %v", r, err)
	}
}

// A template whose instance is no valid mandate (e.g. a creator with control
// characters) fails at admission, not later at evaluation.
func TestAdmitValidatesTheInstance(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	req := request()
	req.By = audit.Actor{Kind: audit.ActorUser, ID: "user\u2028x"}
	if _, _, err := e.adm.Admit(ctx, req); !errors.Is(err, mandate.ErrInvalid) {
		t.Errorf("Admit = %v, want mandate.ErrInvalid", err)
	}
}

func TestAdmissionReportsDatabaseErrors(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.db.Close()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err == nil {
		t.Error("PutTemplate succeeded")
	}
	if _, err := e.adm.Templates(ctx); err == nil {
		t.Error("Templates succeeded")
	}
	if err := e.adm.RemoveTemplate(ctx, "x"); err == nil || errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("RemoveTemplate = %v", err)
	}
	if _, _, err := e.adm.Admit(ctx, request()); err == nil {
		t.Error("Admit succeeded")
	}
}
