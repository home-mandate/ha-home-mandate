// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/home-mandate/home-mandate/internal/mandate"
)

// A mandate never names a placeholder as approver: nobody could approve then.
func TestPutRefusesPlaceholderApprovers(t *testing.T) {
	e := newEnv(t)
	a := e.agent(t, "Claude")
	for name, edit := range map[string]func(map[string]any){
		"mandate level": func(d map[string]any) {
			d["approval"].(map[string]any)["approvers"] = []any{"$approvers"}
		},
		"rule level": func(d map[string]any) {
			for _, r := range d["rules"].([]any) {
				if ap, ok := r.(map[string]any)["approval"].(map[string]any); ok {
					ap["approvers"] = []any{"user-1", "$approvers"}
				}
			}
		},
		"any dollar value": func(d map[string]any) {
			d["approval"].(map[string]any)["approvers"] = []any{"$someone"}
		},
	} {
		_, err := e.mandates.Put(context.Background(), voiceAssistant(t, a.ClientID, edit), admin)
		if !errors.Is(err, mandate.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func approversOf(t *testing.T, doc []byte) (top []string, rules [][]string) {
	t.Helper()
	var d struct {
		Approval struct{ Approvers []string } `json:"approval"`
		Rules    []struct {
			Approval *struct{ Approvers []string } `json:"approval"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	for _, r := range d.Rules {
		if r.Approval != nil {
			rules = append(rules, r.Approval.Approvers)
		}
	}
	return d.Approval.Approvers, rules
}

func TestReplaceApprovers(t *testing.T) {
	doc := []byte(`{"approval":{"timeout":"PT2M","approvers":["$approvers"]},"rules":[` +
		`{"id":"a","approval":{"timeout":"PT1M","approvers":["user-9","$approvers"]}},{"id":"b"}],"x":1}`)
	out, err := mandate.ReplaceApprovers(doc, []string{"user-1", "user-2", "user-1"})
	if err != nil {
		t.Fatal(err)
	}
	top, rules := approversOf(t, out)
	if !slices.Equal(top, []string{"user-1", "user-2"}) {
		t.Errorf("mandate level = %v", top)
	}
	if len(rules) != 1 || !slices.Equal(rules[0], []string{"user-9", "user-1", "user-2"}) {
		t.Errorf("rule level = %v", rules)
	}
	var rest map[string]any
	_ = json.Unmarshal(out, &rest)
	if rest["x"] != float64(1) {
		t.Error("other fields lost")
	}

	// Without a placeholder nothing changes, also without anyone to put there.
	plain := []byte(`{"approval":{"approvers":["user-1"]},"rules":[]}`)
	if out, err := mandate.ReplaceApprovers(plain, nil); err != nil || !json.Valid(out) {
		t.Errorf("no placeholder: %v", err)
	} else if top, _ := approversOf(t, out); !slices.Equal(top, []string{"user-1"}) {
		t.Errorf("no placeholder changed to %v", top)
	}
}

func TestReplaceApproversRefuses(t *testing.T) {
	for name, tc := range map[string]struct {
		doc   string
		users []string
		want  error
	}{
		"nobody to put there":  {`{"approval":{"approvers":["$approvers"]},"rules":[]}`, nil, mandate.ErrNoApprovers},
		"unknown placeholder":  {`{"approval":{"approvers":["$owner"]},"rules":[]}`, []string{"user-1"}, mandate.ErrInvalid},
		"not a JSON object":    {`[1]`, []string{"user-1"}, mandate.ErrInvalid},
		"approvers not a list": {`{"approval":{"approvers":"$approvers"},"rules":[]}`, []string{"user-1"}, mandate.ErrInvalid},
		"approver not a text":  {`{"approval":{"approvers":[1]},"rules":[]}`, []string{"user-1"}, mandate.ErrInvalid},
		"empty user ID":        {`{"approval":{"approvers":["$approvers"]},"rules":[]}`, []string{""}, mandate.ErrNoApprovers},
	} {
		if _, err := mandate.ReplaceApprovers([]byte(tc.doc), tc.users); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}
