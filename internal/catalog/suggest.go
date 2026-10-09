// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"slices"
	"strings"
	"unicode"
)

// suggestCategories are the categories whose actions the vocabulary does not treat as
// critical but which often drive a door: a relay, a cover, a script. Locks, gates and
// alarms are critical anyway; lights, media and sensors do not open anything.
var suggestCategories = map[string]bool{"switch": true, "cover": true, "script": true, "other": true}

// suggestClasses are device classes of a cover that close an opening of the house; doors,
// garage doors and gates are the critical category gate anyway.
var suggestClasses = map[string]bool{"window": true}

// suggestParts are parts of a German word that point to a door or a garage; German
// joins words ("Kellertür"), so they count anywhere in a word.
var suggestParts = []string{"tür", "tuer", "garage"}

// suggestWords are whole words that point to a door, a gate or a garage. English words
// count only whole, so "outdoor" or "navigate" propose nothing.
var suggestWords = map[string]bool{"door": true, "doors": true, "gate": true, "gates": true, "tor": true, "tore": true}

// gateEnds are German compounds ending in "tor" that mean a gate ("Hoftor", "Gartentor");
// not every word that ends in "tor" does ("Motor", "Investor").
var gateEnds = []string{"entor", "hoftor", "rolltor", "schiebetor", "flügeltor", "kipptor", "falttor", "einfahrtstor", "grundstückstor"}

// SuggestCritical reports whether the UI should propose marking the device as critical:
// its device class, name or entity ID points to a door, a gate or a garage. It is only a
// proposal from a fixed word list and misses devices with other names; the household
// decides.
func SuggestCritical(d Device) bool {
	if !suggestCategories[d.Category] {
		return false
	}
	if class, _ := d.Attributes["device_class"].(string); suggestClasses[class] {
		return true
	}
	for _, text := range []string{d.Name(), d.EntityID} {
		for _, word := range strings.FieldsFunc(strings.ToLower(text), func(r rune) bool { return !unicode.IsLetter(r) }) {
			if suggestWords[word] ||
				slices.ContainsFunc(gateEnds, func(end string) bool { return strings.HasSuffix(word, end) }) ||
				slices.ContainsFunc(suggestParts, func(part string) bool { return strings.Contains(word, part) }) {
				return true
			}
		}
	}
	return false
}
