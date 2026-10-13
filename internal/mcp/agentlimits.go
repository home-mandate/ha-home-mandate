// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/home-mandate/spec/evaluator"
)

// limitOut is one limit of the agent's mandate on a parameter, in the units the agent
// uses in perform_action: degrees in the household's unit, percent, a volume level.
type limitOut struct {
	Min  *float64 `json:"min,omitempty"`
	Max  *float64 `json:"max,omitempty"`
	Unit string   `json:"unit,omitempty"`
}

// agentLimits turns the limits of the allow rules that could allow an action (one map per
// rule, pdp.Snapshot.Limits) into the agent's parameters. A rule limiting a value the
// agent cannot give in this household (a temperature in the other unit, or any while the
// unit is unknown) never applies and is left out; a rule without limits is an empty map.
func agentLimits(rules []map[string]evaluator.Limit, category, action, unit string) []map[string]limitOut {
	var out []map[string]limitOut
	for _, rule := range rules {
		limits := map[string]limitOut{}
		usable := true
		for name, l := range rule {
			callName, scale, ok := agentParameter(category, action, name, unit)
			if !ok {
				usable = false
				break
			}
			limits[callName] = limitOut{Min: scaled(l.Min, scale), Max: scaled(l.Max, scale), Unit: unitLabel(callName, unit)}
		}
		if usable {
			out = append(out, limits)
		}
	}
	return out
}

// agentParameter is the parameter of perform_action that the evaluation parameter name
// comes from in this household, and its scale.
func agentParameter(category, action, name, unit string) (string, float64, bool) {
	for callName, p := range evaluationParameterOf[category][action] {
		evaluated := p.name
		if p.perUnit != nil {
			evaluated = p.perUnit[unit]
		}
		if evaluated != "" && evaluated == name {
			return callName, p.scale, true
		}
	}
	return "", 0, false
}

func scaled(v *int64, scale float64) *float64 {
	if v == nil {
		return nil
	}
	f := float64(*v) / scale
	return &f
}

// unitLabel is the unit an agent's parameter is given in; none for a volume level (0 to 1).
func unitLabel(callName, temperatureUnit string) string {
	switch callName {
	case "temperature":
		return temperatureUnit
	case "brightness_pct", "position":
		return "%"
	}
	return ""
}

// unlimited tells whether one of the rules allows the action with any value.
func unlimited(limits []map[string]limitOut) bool {
	for _, l := range limits {
		if len(l) == 0 {
			return true
		}
	}
	return false
}

// limitsText says in a refusal what the agent's mandate allows, e.g. "temperature 16 to
// 23 °C"; alternatives of several rules are joined with "or". Empty without limits.
func limitsText(limits []map[string]limitOut) string {
	var alternatives []string
	for _, rule := range limits {
		var parts []string
		for _, name := range slices.Sorted(maps.Keys(rule)) {
			l := rule[name]
			unit := ""
			if l.Unit != "" {
				unit = " " + l.Unit
			}
			switch {
			case l.Min != nil && l.Max != nil:
				parts = append(parts, name+" "+formatNumber(*l.Min)+" to "+formatNumber(*l.Max)+unit)
			case l.Min != nil:
				parts = append(parts, name+" from "+formatNumber(*l.Min)+unit)
			case l.Max != nil:
				parts = append(parts, name+" up to "+formatNumber(*l.Max)+unit)
			}
		}
		if len(parts) > 0 {
			alternatives = append(alternatives, strings.Join(parts, " and "))
		}
	}
	return strings.Join(alternatives, " or ")
}

func formatNumber(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
