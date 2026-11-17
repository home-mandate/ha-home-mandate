// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"encoding/json"
	"strings"
	"unicode/utf8"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

const (
	// maxReadState is the longest state read returns, in characters. Longer states are free
	// text an integration fills, not a state (profiles/home-assistant.md "What read returns").
	maxReadState = 64
	// maxReadAttribute is the longest string attribute read returns, in characters: the
	// listed attributes are modes, classes and units, all much shorter.
	maxReadAttribute = 32
	// maxReadList is the most items of a list attribute read returns.
	maxReadList = 32
	// minBareKey is the length from which a single word of key characters counts as a key
	// or token rather than a state.
	minBareKey = 24
)

// readAttributes are the attributes read returns per category: exactly the list of the
// Home Assistant profile (SPEC-v0 section 11.8). Every other attribute stays with Home
// Assistant; entity_picture, access_token, code_format and changed_by in particular.
var readAttributes = map[string][]string{
	"light":   {"brightness", "color_mode", "color_temp_kelvin", "supported_color_modes"},
	"switch":  nil,
	"climate": {"hvac_action", "current_temperature", "temperature", "target_temp_low", "target_temp_high", "hvac_modes", "min_temp", "max_temp"},
	"cover":   {"current_position"},
	"gate":    {"current_position"},
	"lock":    nil,
	"alarm":   nil,
	"camera":  nil,
	"media":   {"volume_level", "is_volume_muted"},
	"sensor":  {"unit_of_measurement", "device_class"},
	"scene":   nil,
	"script":  nil,
}

// readOtherDomains are the domains of category other whose state read returns; for every
// other domain (text, person, device_tracker, update, ...) it returns nothing.
var readOtherDomains = map[string]bool{
	"fan": true, "vacuum": true, "valve": true, "humidifier": true, "water_heater": true, "siren": true,
	"input_boolean": true, "input_number": true, "input_select": true, "number": true, "select": true,
}

// readView is what read gives an agent of a device: the state and the attributes the
// profile lists for its category, nothing else. Beyond the profile, which leaves out only
// sensor states with "://" or more than 64 characters, it leaves out in every category a
// state or string that looks like an address, credentials or a key (see plainText), and
// keeps attribute values only as short scalars or short lists of them. An empty state means
// that read returns none. The device is not modified.
func readView(dev catalog.Device) (string, map[string]any) {
	keys, ok := readAttributes[dev.Category]
	if dev.Category == "other" {
		domain, _, _ := strings.Cut(dev.EntityID, ".")
		ok = readOtherDomains[domain]
	}
	if !ok {
		return "", nil
	}
	var attrs map[string]any
	for _, k := range keys {
		v, present := dev.Attributes[k]
		if !present || !plainValue(v) {
			continue
		}
		if attrs == nil {
			attrs = make(map[string]any, len(keys))
		}
		attrs[k] = v
	}
	return readState(dev.State), attrs
}

// readState is the state as read returns it: empty if it is long or not plain text.
func readState(state string) string {
	if utf8.RuneCountInString(state) > maxReadState || !plainText(state) {
		return ""
	}
	return state
}

// plainText reports whether s carries no address, credentials or key: no "//" (a URL with
// or without a scheme), no "@" (credentials in a URL, an e-mail address), no backslash (a
// network share), and is not a single long word of key characters (a bare token or key).
func plainText(s string) bool {
	if strings.Contains(s, "//") || strings.ContainsAny(s, `@\`) {
		return false
	}
	return !bareKey(s)
}

// bareKey reports whether s is one word of at least minBareKey characters from the
// alphabets of hexadecimal, base64 and base64url. Timestamps (":") and text (" ") are not.
func bareKey(s string) bool {
	if len(s) < minBareKey {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '_' || r == '-' || r == '+' || r == '/' || r == '=' || r == '.':
		default:
			return false
		}
	}
	return true
}

// plainValue reports whether v is a short scalar, or a short list of short scalars.
func plainValue(v any) bool {
	if list, ok := v.([]any); ok {
		if len(list) > maxReadList {
			return false
		}
		for _, item := range list {
			if !plainScalar(item) {
				return false
			}
		}
		return true
	}
	return plainScalar(v)
}

func plainScalar(v any) bool {
	switch x := v.(type) {
	case nil, bool, float64, float32, int, int64, json.Number:
		return true
	case string:
		return utf8.RuneCountInString(x) <= maxReadAttribute && !strings.ContainsAny(x, ":?=") && plainText(x)
	default:
		return false
	}
}
