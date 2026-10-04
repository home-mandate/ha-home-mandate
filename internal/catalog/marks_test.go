// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import "testing"

type fixedMarks map[string]bool

func (m fixedMarks) Critical(id string) bool { return m[id] }

func TestCatalogReportsTheMarks(t *testing.T) {
	c := loaded(t, house())
	all := c.All()
	if len(all) == 0 {
		t.Fatal("the test catalog is empty")
	}
	id := all[0].EntityID
	if d, _ := c.Lookup(id); d.Critical || all[0].Critical {
		t.Fatal("an entity is critical without marks")
	}
	c.SetMarks(fixedMarks{id: true})
	if d, ok := c.Lookup(id); !ok || !d.Critical {
		t.Errorf("Lookup(%s).Critical = false after marking", id)
	}
	for _, d := range c.All() {
		if d.Critical != (d.EntityID == id) {
			t.Errorf("All: %s critical = %v", d.EntityID, d.Critical)
		}
	}
}
