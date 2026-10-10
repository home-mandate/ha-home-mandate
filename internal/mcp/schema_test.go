// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/ha-home-mandate/internal/approval"
)

// Output schemas (issue #27, acceptance test 2026-10-10): clients cache tools/list across
// server updates and validate structuredContent against the cached outputSchema, so an
// output schema must accept fields added later (no additionalProperties: false), while it
// still declares every field a result has today.

// schemas returns, per tool, the output schema the server advertises and a strict copy.
func schemas(t *testing.T, s *sdk.ClientSession) (open, strict map[string]*jsonschema.Resolved) {
	t.Helper()
	res, err := s.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	open, strict = map[string]*jsonschema.Resolved{}, map[string]*jsonschema.Resolved{}
	for _, tool := range res.Tools {
		data, _ := json.Marshal(tool.OutputSchema)
		if strings.Contains(string(data), `"additionalProperties":false`) {
			t.Errorf("%s: output schema closed: %s", tool.Name, data)
		}
		var o, c jsonschema.Schema
		if err := json.Unmarshal(data, &o); err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		_ = json.Unmarshal(data, &c)
		closeObjects(&c)
		for name, sch := range map[string]*jsonschema.Schema{"open": &o, "strict": &c} {
			r, err := sch.Resolve(nil)
			if err != nil {
				t.Fatalf("%s %s: %v", tool.Name, name, err)
			}
			if name == "open" {
				open[tool.Name] = r
			} else {
				strict[tool.Name] = r
			}
		}
	}
	return open, strict
}

// closeObjects forbids undeclared fields on every object with declared properties.
func closeObjects(s *jsonschema.Schema) {
	if s == nil {
		return
	}
	if len(s.Properties) > 0 {
		s.AdditionalProperties = &jsonschema.Schema{Not: &jsonschema.Schema{}}
	}
	for _, p := range s.Properties {
		closeObjects(p)
	}
	closeObjects(s.Items)
}

// Every result path's structured content conforms to the advertised schema and declares
// no field the schema lacks.
func TestOutputSchemasCoverEveryResult(t *testing.T) {
	h, f := pendingHarness(t)
	s := h.session()
	open, strict := schemas(t, s)
	seen := map[string]int{}
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		if res.StructuredContent == nil {
			return nil
		}
		data, _ := json.Marshal(res.StructuredContent)
		var v any
		_ = json.Unmarshal(data, &v)
		for name, r := range map[string]*jsonschema.Resolved{"advertised": open[tool], "strict": strict[tool]} {
			if err := r.Validate(v); err != nil {
				t.Errorf("%s %s: %s does not match: %v", tool, name, data, err)
			}
		}
		seen[tool]++
		out, _ := v.(map[string]any)
		return out
	}
	unlock := func(entity, key string) map[string]any {
		args := map[string]any{"entity_id": entity, "action": "unlock"}
		if key != "" {
			args["idempotency_key"] = key
		}
		return call("perform_action", args)
	}
	call("list_devices", nil)
	call("list_my_permissions", nil)
	call("get_state", map[string]any{"entity_id": "light.kitchen"})
	call("perform_action", map[string]any{"entity_id": "light.kitchen", "action": "turn_on"}) // executed
	h.ha.onCall = func() { h.setState("lock.front_door", "unlocked") }
	p := unlock("lock.front_door", "order-0001") // pending, waiting
	ref := p["approval_id"]
	unlock("lock.front_door", "order-0001")                                                  // idempotent attach
	call("perform_action", map[string]any{"entity_id": "lock.front_door", "action": "open"}) // approval_pending with retry
	call("approval_status", map[string]any{"approval_id": ref})                              // pending
	f.end(fmt.Sprintf("%032x", 1), approvedNow(h))
	waitFor(t, func() bool { return len(h.ha.recorded()) == 2 })
	waitFor(t, func() bool {
		return call("approval_status", map[string]any{"approval_id": ref})["status"] == "executed"
	})
	unlock("lock.front_door", "")                                            // already_executed (replay)
	unlock("lock.front_door", "order-0001")                                  // already_executed (key)
	b := unlock("lock.back_door", "")                                        // pending
	call("approval_cancel", map[string]any{"approval_id": b["approval_id"]}) // withdrawn
	unlock("lock.back_door", "")                                             // cooldown refusal with retry
	unlock("lock.garden_gate", "")                                           // pending
	h.now.advance(resultKeep + time.Minute)
	call("approval_status", map[string]any{"approval_id": ref}) // journal: executed
	f.end(fmt.Sprintf("%032x", 3), approval.Result{Outcome: approval.OutcomeApproved, By: approverID, At: h.now.Now()})
	for _, tool := range []string{"list_devices", "list_my_permissions", "get_state", "perform_action", "approval_status", "approval_cancel"} {
		if seen[tool] == 0 {
			t.Errorf("no structured result of %s checked", tool)
		}
	}
	if seen["perform_action"] < 7 {
		t.Errorf("only %d perform_action results checked", seen["perform_action"])
	}
}

// The answered phase (the execution outlasts the grace) is a result path too.
func TestOutputSchemaOfTheAnsweredPhase(t *testing.T) {
	h, f := pendingHarness(t)
	s := h.session()
	open, strict := schemas(t, s)
	old := answerGrace
	answerGrace = 50 * time.Millisecond
	defer func() { answerGrace = old }()
	release := make(chan struct{})
	h.ha.onCall = func() { <-release }
	defer close(release)
	h.gw.mu.Lock()
	h.gw.cfg.ApprovalWait = 2 * time.Second
	h.gw.mu.Unlock()
	go func() {
		waitFor(t, func() bool { return len(f.heldIDs()) == 1 })
		f.end(f.heldIDs()[0], approvedNow(h))
	}()
	res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "perform_action", Arguments: map[string]any{"entity_id": "lock.front_door", "action": "unlock"}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(res.StructuredContent)
	var v any
	_ = json.Unmarshal(data, &v)
	if v.(map[string]any)["phase"] != "answered" {
		t.Fatalf("result = %s", data)
	}
	for _, r := range []*jsonschema.Resolved{open["perform_action"], strict["perform_action"]} {
		if err := r.Validate(v); err != nil {
			t.Errorf("%s: %v", data, err)
		}
	}
}
