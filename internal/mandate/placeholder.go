// SPDX-License-Identifier: AGPL-3.0-or-later

package mandate

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ApproversPlaceholder stands, in a mandate template, for the human who admits the agent
// plus every approver set up in Home-Mandate (ARCHITECTURE section 6). Admission replaces
// it; a mandate never contains it, nor any other approver starting with "$".
const ApproversPlaceholder = "$approvers"

// ErrNoApprovers means a template names the placeholder, but there is nobody to put there.
var ErrNoApprovers = errors.New("mandate: nobody to approve")

// ReplaceApprovers puts approvers in place of ApproversPlaceholder, on the mandate and on
// every rule, without duplicates and keeping the order. Any other value starting with "$"
// is refused, as is the placeholder without anyone to put there.
func ReplaceApprovers(document []byte, approvers []string) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(document, &doc); err != nil {
		return nil, fmt.Errorf("%w: not a JSON object", ErrInvalid)
	}
	replace := func(approval any) error {
		ap, ok := approval.(map[string]any)
		if !ok {
			return nil // a missing or malformed approval is the schema's to refuse
		}
		list, present := ap["approvers"]
		if !present {
			return nil
		}
		values, ok := list.([]any)
		if !ok {
			return fmt.Errorf("%w: approvers is not a list", ErrInvalid)
		}
		out := make([]any, 0, len(values)+len(approvers))
		seen := map[string]bool{}
		add := func(v string) {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		for _, v := range values {
			s, ok := v.(string)
			switch {
			case !ok:
				return fmt.Errorf("%w: an approver is not a text", ErrInvalid)
			case s == ApproversPlaceholder:
				if !slices.ContainsFunc(approvers, func(a string) bool { return a != "" }) {
					return ErrNoApprovers
				}
				for _, a := range approvers {
					if a != "" {
						add(a)
					}
				}
			case strings.HasPrefix(s, "$"):
				return fmt.Errorf("%w: unknown placeholder %q", ErrInvalid, s)
			default:
				add(s)
			}
		}
		ap["approvers"] = out
		return nil
	}
	if err := replace(doc["approval"]); err != nil {
		return nil, err
	}
	if rules, ok := doc["rules"].([]any); ok {
		for _, r := range rules {
			if rule, ok := r.(map[string]any); ok {
				if err := replace(rule["approval"]); err != nil {
					return nil, err
				}
			}
		}
	}
	return json.Marshal(doc)
}

// placeholderApprover reports an approver starting with "$" in a mandate.
func placeholderApprover(document []byte) error {
	var d struct {
		Approval struct {
			Approvers []string `json:"approvers"`
		} `json:"approval"`
		Rules []struct {
			Approval *struct {
				Approvers []string `json:"approvers"`
			} `json:"approval"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(document, &d); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	lists := [][]string{d.Approval.Approvers}
	for _, r := range d.Rules {
		if r.Approval != nil {
			lists = append(lists, r.Approval.Approvers)
		}
	}
	for _, l := range lists {
		for _, a := range l {
			if strings.HasPrefix(a, "$") {
				return fmt.Errorf("%w: approver %q is a placeholder, not a person", ErrInvalid, a)
			}
		}
	}
	return nil
}
