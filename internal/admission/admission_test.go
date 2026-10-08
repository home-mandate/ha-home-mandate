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
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/spec"

	"github.com/home-mandate/ha-home-mandate/internal/admission"
	"github.com/home-mandate/ha-home-mandate/internal/agent"
	"github.com/home-mandate/ha-home-mandate/internal/audit"
	"github.com/home-mandate/ha-home-mandate/internal/mandate"
	"github.com/home-mandate/ha-home-mandate/internal/store"
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
	mandates := mandate.New(st.DB(), log, household, "urn:uuid:5b0c9f4e-8f1a-4c2e-9d3b-7a6e5f4d3c2b")
	adm := admission.New(st.DB(), agents, mandates, household)
	adm.SetClock(func() time.Time { return now })
	return env{adm: adm, agents: agents, mandates: mandates, log: log, db: st.DB()}
}

// template is the voice assistant example of the specification, edited by edit.
func template(t *testing.T, edit func(map[string]any)) []byte {
	t.Helper()
	data, err := fs.ReadFile(spec.FS(), "examples/voice-assistant.json")
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
	list = own(list)
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
	if list, _ := e.adm.Templates(ctx); len(own(list)) != 1 {
		t.Errorf("templates = %+v", list)
	}
}

// own leaves out the base templates.
func own(list []admission.Template) []admission.Template {
	var out []admission.Template
	for _, t := range list {
		if !t.Builtin {
			out = append(out, t)
		}
	}
	return out
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
		"reserved prefix":    {"hm-mine", template(t, nil)},
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

// criticalTemplate allows unlocking without approval.
func criticalTemplate(t *testing.T) []byte {
	return template(t, func(doc map[string]any) {
		doc["rules"] = []any{map[string]any{"id": "r-unlock", "resource": map[string]any{"category": "lock"}, "actions": []any{"unlock"},
			"decision": "allow", "allow_critical": true}}
	})
}

// A template whose rules allow critical actions without approval admits nobody without
// the separate confirmation (decision U9, also for admissions).
func TestAdmitNeedsConfirmationForCriticalTemplates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "doors", criticalTemplate(t), admin); err != nil {
		t.Fatal(err)
	}
	req := request()
	req.Template = "doors"
	if _, _, err := e.adm.Admit(ctx, req); !errors.Is(err, mandate.ErrCriticalConfirmation) {
		t.Fatalf("Admit without confirmation = %v", err)
	}
	if list, _ := e.agents.List(ctx); len(list) != 0 {
		t.Fatalf("agent admitted without confirmation: %v", list)
	}
	req.ConfirmCritical, req.MandateName = true, "  Türen  "
	a, _, err := e.adm.Admit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := e.mandates.ForAgent(ctx, a.ClientID)
	if err != nil {
		t.Fatal(err)
	}
	if info, _ := e.mandates.Get(ctx, loaded.Info.ID); info.Name != "Türen" {
		t.Errorf("mandate name = %q", info.Name)
	}
}

// A new mandate is named after its agent unless the human gives another name; its first
// version names the template and the digest of its content.
func TestAdmitNamesTheMandateAfterTheAgent(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	_, tmpl, err := e.adm.TemplateDocument(ctx, "voice-assistant")
	if err != nil {
		t.Fatal(err)
	}
	req := request()
	req.RedirectURIs = []string{"https://claude.example.org/callback"}
	a, _, err := e.adm.Admit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := e.mandates.List(ctx)
	if len(list) != 1 || list[0].Name != "Claude" {
		t.Errorf("mandates = %+v", list)
	}
	if got, _ := e.agents.Get(ctx, a.ClientID); len(got.RedirectURIs) != 1 {
		t.Errorf("redirect URIs = %v", got.RedirectURIs)
	}
	want := mandate.Origin{Kind: mandate.OriginTemplate, Template: "voice-assistant", TemplateDigest: tmpl.Digest}
	if v, err := e.mandates.Versions(ctx, list[0].ID); err != nil || len(v) != 1 || v[0].Origin != want {
		t.Errorf("versions = %+v, %v; want origin %+v", v, err, want)
	}
	// A name the human gives wins, for a base template too.
	named := request()
	named.DisplayName, named.OAuthClient, named.Template, named.MandateName = "Garten", "garden", "hm-read-only", "  Nur lesen  "
	b, _, err := e.adm.Admit(ctx, named)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := e.mandates.ForAgent(ctx, b.ClientID)
	_, base, _ := e.adm.TemplateDocument(ctx, "hm-read-only")
	if got, _ := e.mandates.Get(ctx, m.Info.ID); got.Name != "Nur lesen" {
		t.Errorf("named mandate = %+v", got)
	}
	if v, _ := e.mandates.Versions(ctx, m.Info.ID); len(v) != 1 || v[0].Origin != (mandate.Origin{Kind: mandate.OriginTemplate, Template: "hm-read-only", TemplateDigest: base.Digest}) {
		t.Errorf("origin from a base template = %+v", v)
	}
}

func TestNewMandate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.PutTemplate(ctx, "voice-assistant", template(t, nil), admin); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.PutTemplate(ctx, "doors", criticalTemplate(t), admin); err != nil {
		t.Fatal(err)
	}
	a, _, err := e.adm.Admit(ctx, request())
	if err != nil {
		t.Fatal(err)
	}
	// The agent has an active mandate: conflict.
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "voice-assistant", "", false, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("NewMandate with an active mandate = %v", err)
	}
	first, _ := e.mandates.ForAgent(ctx, a.ClientID)
	if err := e.mandates.Revoke(ctx, first.Info.ID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "nope", "", false, admin); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("unknown template = %v", err)
	}
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "doors", "", false, admin); !errors.Is(err, mandate.ErrCriticalConfirmation) {
		t.Errorf("critical template without confirmation = %v", err)
	}
	info, err := e.adm.NewMandate(ctx, a.ClientID, "voice-assistant", "Zweites", false, admin)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID == first.Info.ID || !strings.HasPrefix(info.ID, first.Info.ID+"-") || info.Name != "Zweites" {
		t.Errorf("new mandate = %+v (first %s)", info, first.Info.ID)
	}
	if v, _ := e.mandates.Versions(ctx, info.ID); len(v) != 1 || v[0].Origin.Kind != mandate.OriginTemplate || v[0].Origin.Template != "voice-assistant" {
		t.Errorf("origin of the new mandate = %+v", v)
	}
	now, _ := e.mandates.ForAgent(ctx, a.ClientID)
	if now.Info.ID != info.ID || now.Info.Status != mandate.StatusActive {
		t.Errorf("ForAgent = %+v, want the new mandate", now.Info)
	}
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "voice-assistant", "", false, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("third mandate = %v", err)
	}
	// Without a name, the new mandate is named after the agent.
	if err := e.mandates.Revoke(ctx, info.ID, admin); err != nil {
		t.Fatal(err)
	}
	if third, err := e.adm.NewMandate(ctx, a.ClientID, "voice-assistant", " ", false, admin); err != nil || third.Name != a.DisplayName {
		t.Errorf("new mandate without a name = %+v, %v; want the name %q", third, err, a.DisplayName)
	}
	if err := e.agents.Revoke(ctx, a.ClientID, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := e.adm.NewMandate(ctx, a.ClientID, "voice-assistant", "", false, admin); !errors.Is(err, admission.ErrAgentNotActive) {
		t.Errorf("revoked agent = %v", err)
	}
	if _, err := e.adm.NewMandate(ctx, "hm-client:nobody", "voice-assistant", "", false, admin); !errors.Is(err, admission.ErrAgentNotActive) {
		t.Errorf("unknown agent = %v", err)
	}
}

func TestUpdateTemplateNeedsConfirmationForNewCriticalRules(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.UpdateTemplate(ctx, "doors", criticalTemplate(t), "", false, admin); !errors.Is(err, mandate.ErrCriticalConfirmation) {
		t.Fatalf("new critical template = %v", err)
	}
	if err := e.adm.UpdateTemplate(ctx, "doors", criticalTemplate(t), "", true, admin); err != nil {
		t.Fatal(err)
	}
	doc, tmpl, err := e.adm.TemplateDocument(ctx, "doors")
	if err != nil || !bytes.Contains(doc, []byte("allow_critical")) || tmpl.Name != "doors" || tmpl.CreatedBy != admin.ID || tmpl.CreatedAt.IsZero() ||
		!strings.HasPrefix(tmpl.Digest, "sha256:") {
		t.Errorf("TemplateDocument = %s %+v %v", doc, tmpl, err)
	}
	// Unchanged critical rule: no new confirmation.
	if err := e.adm.UpdateTemplate(ctx, "doors", criticalTemplate(t), tmpl.Digest, false, admin); err != nil {
		t.Errorf("unchanged critical rule = %v", err)
	}
	if _, _, err := e.adm.TemplateDocument(ctx, "none"); !errors.Is(err, admission.ErrTemplateNotFound) {
		t.Errorf("unknown template = %v", err)
	}
	_, tmpl, _ = e.adm.TemplateDocument(ctx, "doors")
	if err := e.adm.UpdateTemplate(ctx, "doors", []byte(`not json`), tmpl.Digest, false, admin); !errors.Is(err, admission.ErrInvalidTemplate) {
		t.Errorf("invalid template = %v", err)
	}
}

// Nobody overwrites a version of a template they have not seen.
func TestUpdateTemplateNeedsTheVersionTheEditStartedFrom(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.UpdateTemplate(ctx, "mine", template(t, nil), "", false, admin); err != nil {
		t.Fatal(err)
	}
	_, first, _ := e.adm.TemplateDocument(ctx, "mine")
	edited := template(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10} })
	for name, base := range map[string]string{
		"as new although it exists": "",
		"an unknown version":        "sha256:" + strings.Repeat("0", 64),
	} {
		if err := e.adm.UpdateTemplate(ctx, "mine", edited, base, false, admin); !errors.Is(err, mandate.ErrConflict) {
			t.Errorf("%s: %v, want ErrConflict", name, err)
		}
	}
	if err := e.adm.UpdateTemplate(ctx, "mine", edited, first.Digest, false, admin); err != nil {
		t.Fatal(err)
	}
	// The first version is now outdated.
	if err := e.adm.UpdateTemplate(ctx, "mine", template(t, nil), first.Digest, false, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("edit of an outdated version = %v", err)
	}
	if err := e.adm.RemoveTemplate(ctx, "mine"); err != nil {
		t.Fatal(err)
	}
	if err := e.adm.UpdateTemplate(ctx, "mine", edited, first.Digest, false, admin); !errors.Is(err, mandate.ErrConflict) {
		t.Errorf("edit of a removed template = %v", err)
	}
}

func TestInvalidTemplates(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, name := range []string{"voice-assistant", "energy"} {
		if err := e.adm.PutTemplate(ctx, name, template(t, nil), admin); err != nil {
			t.Fatal(err)
		}
	}
	if invalid, err := e.adm.InvalidTemplates(ctx); err != nil || len(invalid) != 0 {
		t.Fatalf("InvalidTemplates on valid templates = %v, %v", invalid, err)
	}
	if _, err := e.db.Exec(`UPDATE mandate_templates SET document = replace(document, '"default"', '"unknown_member":1,"default"') WHERE name = 'energy'`); err != nil {
		t.Fatal(err)
	}
	invalid, err := e.adm.InvalidTemplates(ctx)
	if err != nil || len(invalid) != 1 || invalid[0].Name != "energy" || invalid[0].Problem == "" {
		t.Fatalf("InvalidTemplates = %+v, %v; want energy", invalid, err)
	}
	if _, err := e.db.Exec(`UPDATE mandate_templates SET document = 'nope' WHERE name = 'voice-assistant'`); err != nil {
		t.Fatal(err)
	}
	if invalid, _ := e.adm.InvalidTemplates(ctx); len(invalid) != 2 {
		t.Errorf("InvalidTemplates with a template that is no JSON = %+v", invalid)
	}
}

// Edits of the same version at the same time: exactly one is stored, the others are
// conflicts; also for new templates of the same name.
func TestConcurrentTemplateEditsStoreExactlyOne(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	if err := e.adm.UpdateTemplate(ctx, "shared", template(t, nil), "", false, admin); err != nil {
		t.Fatal(err)
	}
	_, base, _ := e.adm.TemplateDocument(ctx, "shared")
	for name, tc := range map[string]struct{ template, base string }{
		"update of one version": {"shared", base.Digest},
		"two new templates":     {"fresh", ""},
	} {
		var wg sync.WaitGroup
		results := make(chan error, 8)
		for i := range 8 {
			wg.Go(func() {
				doc := template(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 10 + i} })
				results <- e.adm.UpdateTemplate(ctx, tc.template, doc, tc.base, false, admin)
			})
		}
		wg.Wait()
		close(results)
		stored := 0
		for err := range results {
			switch {
			case err == nil:
				stored++
			case !errors.Is(err, mandate.ErrConflict):
				t.Errorf("%s: %v", name, err)
			}
		}
		if stored != 1 {
			t.Errorf("%s: %d edits stored, want exactly 1", name, stored)
		}
	}
}
