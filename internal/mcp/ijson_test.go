// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A value that is not I-JSON is not a request: it is rejected at the interface, before any
// evaluation, and leaves no decision entry (SPEC-v0 section 9.1).
func TestArgumentsThatAreNotIJSONAreRejected(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	before := h.auditLog()
	for name, tc := range map[string]struct{ tool, args string }{
		"noncharacter in reason":   {"get_state", `{"entity_id":"light.kitchen","reason":"a￾"}`},
		"noncharacter, raw":        {"get_state", "{\"entity_id\":\"light.kitchen\",\"reason\":\"a﷐\"}"},
		"lone surrogate in reason": {"get_state", `{"entity_id":"light.kitchen","reason":"a\ud800"}`},
		"duplicate member":         {"get_state", `{"entity_id":"light.kitchen","entity_id":"lock.front_door"}`},
		"noncharacter in a param":  {"perform_action", `{"entity_id":"light.kitchen","action":"set","params":{"brightness_pct":50,"x":"￿"}}`},
		"lone surrogate in a list": {"list_devices", `{"search":"\udc00"}`},
	} {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: tc.tool, Arguments: json.RawMessage(tc.args)})
		if err != nil {
			t.Errorf("%s: protocol error %v", name, err)
			continue
		}
		var text strings.Builder
		for _, c := range res.Content {
			if tc, ok := c.(*sdk.TextContent); ok {
				text.WriteString(tc.Text)
			}
		}
		if !res.IsError || !strings.Contains(text.String(), codeInvalidParams) {
			t.Errorf("%s: error %t, %q, want %s", name, res.IsError, text.String(), codeInvalidParams)
		}
	}
	if after := h.auditLog(); after != before {
		t.Errorf("entries were written:\n%s", strings.TrimPrefix(after, before))
	}
}

// Characters next to the noncharacters are ordinary input.
func TestArgumentsThatAreIJSONPass(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	for _, args := range []string{`{"entity_id":"light.kitchen","reason":"a�"}`, `{"entity_id":"light.kitchen","reason":"😀"}`} {
		res, err := s.CallTool(context.Background(), &sdk.CallToolParams{Name: "get_state", Arguments: json.RawMessage(args)})
		if err != nil || res.IsError {
			t.Errorf("%s: %v, %+v", args, err, res)
		}
	}
}
