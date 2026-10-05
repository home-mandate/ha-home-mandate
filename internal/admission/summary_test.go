// SPDX-License-Identifier: AGPL-3.0-or-later

package admission_test

import (
	"reflect"
	"testing"

	"github.com/home-mandate/home-mandate/internal/admission"
)

func TestSummarize(t *testing.T) {
	doc := []byte(`{"rules":[
		{"id":"a","resource":{"any":true},"actions":["read"],"decision":"allow"},
		{"id":"b","resource":{"category":"light","area":"kitchen"},"actions":["turn_on","turn_off"],"decision":"allow","conditions":{"time":[]}},
		{"id":"c","resource":{"category":"lock"},"actions":["unlock","open"],"decision":"ask"},
		{"id":"d","resource":{"entity_id":"lock.front_door"},"actions":["unlock"],"decision":"allow","allow_critical":true},
		{"id":"e","resource":{"area":"garden"},"actions":["*"],"decision":"deny"}
	],"default":"deny"}`)
	got, err := admission.Summarize(doc)
	if err != nil {
		t.Fatal(err)
	}
	want := admission.Summary{
		Allow: []admission.SummaryLine{
			{Any: true, Actions: []string{"read"}},
			{Category: "light", Area: "kitchen", Actions: []string{"turn_on", "turn_off"}, Conditions: true},
			{Entity: "lock.front_door", Actions: []string{"unlock"}, Critical: true},
		},
		Ask:  []admission.SummaryLine{{Category: "lock", Actions: []string{"unlock", "open"}}},
		Deny: []admission.SummaryLine{{Area: "garden", All: true}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("summary =\n%+v\nwant\n%+v", got, want)
	}
}

func TestSummarizeRefusesWhatIsNoMandate(t *testing.T) {
	for _, doc := range []string{`{`, `[]`, `{"rules":"x"}`, `{"rules":[{"decision":"maybe","resource":{"any":true},"actions":["read"]}]}`} {
		if _, err := admission.Summarize([]byte(doc)); err == nil {
			t.Errorf("%s: no error", doc)
		}
	}
}

// Every base template can be summarized, and says what it allows.
func TestBaseTemplatesSummarize(t *testing.T) {
	e := newEnv(t)
	for _, name := range baseNames {
		doc, _, err := e.adm.TemplateDocument(t.Context(), name)
		if err != nil {
			t.Fatal(err)
		}
		s, err := admission.Summarize(doc)
		if err != nil || len(s.Allow) == 0 {
			t.Errorf("%s: %+v, %v", name, s, err)
		}
	}
}

func TestSummarizeEmptyConditionsRestrictNothing(t *testing.T) {
	for _, c := range []string{`null`, `{}`, `[]`} {
		s, err := admission.Summarize([]byte(`{"rules":[{"resource":{"any":true},"actions":["read"],"decision":"allow","conditions":` + c + `}]}`))
		if err != nil || s.Allow[0].Conditions {
			t.Errorf("conditions %s: %+v, %v", c, s, err)
		}
	}
}
