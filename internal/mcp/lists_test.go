// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

// listedIDs returns the entity IDs of a list_devices page.
func listedIDs(out map[string]any) []string {
	var ids []string
	devices, _ := out["devices"].([]any)
	for _, d := range devices {
		ids = append(ids, d.(map[string]any)["entity_id"].(string))
	}
	return ids
}

// permissionsByDevice expands the groups of a list_my_permissions page to the actions
// of each device.
func permissionsByDevice(out map[string]any) map[string]map[string]any {
	got := map[string]map[string]any{}
	groups, _ := out["groups"].([]any)
	for _, g := range groups {
		m := g.(map[string]any)
		for _, id := range m["entity_ids"].([]any) {
			got[id.(string)] = m["actions"].(map[string]any)
		}
	}
	return got
}

// Issue #36: filters narrow both lists, and never name a device the agent may not read.
func TestListFilters(t *testing.T) {
	readable := []string{"alarm_control_panel.home", "climate.living_room", "light.kitchen", "lock.back_door", "lock.front_door", "lock.garden_gate", "number.wallbox"}
	tests := []struct {
		name string
		args map[string]any
		want []string
	}{
		{"no filter", nil, readable},
		{"area", map[string]any{"area": "garden"}, []string{"lock.back_door", "lock.garden_gate"}},
		{"category", map[string]any{"category": "lock"}, []string{"lock.back_door", "lock.front_door", "lock.garden_gate"}},
		{"area and category", map[string]any{"area": "hallway", "category": "lock"}, []string{"lock.front_door"}},
		{"floor", map[string]any{"area": "floor:ground"}, []string{"light.kitchen", "lock.front_door"}},
		{"search in the entity ID, any case", map[string]any{"search": "GARDEN"}, []string{"lock.garden_gate"}},
		{"search in the name", map[string]any{"search": "itche"}, []string{"light.kitchen"}},
		{"area of an unreadable device", map[string]any{"area": "porch"}, nil},
		{"category of an unreadable device", map[string]any{"category": "camera"}, nil},
		{"search for an unreadable device", map[string]any{"search": "camera"}, nil},
		{"unknown area", map[string]any{"area": "attic"}, nil},
	}
	h := newHarness(t, nil)
	s := h.session()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errText := h.call(s, "list_devices", tt.args)
			if errText != "" {
				t.Fatal(errText)
			}
			if got := listedIDs(out); !slices.Equal(got, tt.want) || out["total"] != float64(len(tt.want)) {
				t.Errorf("list_devices = %v (total %v), want %v", got, out["total"], tt.want)
			}
			perms, errText := h.call(s, "list_my_permissions", tt.args)
			if errText != "" {
				t.Fatal(errText)
			}
			got := permissionsByDevice(perms)
			if len(got) != len(tt.want) || perms["total"] != float64(len(tt.want)) {
				t.Errorf("list_my_permissions = %v (total %v), want %v", got, perms["total"], tt.want)
			}
			for _, id := range tt.want {
				if _, ok := got[id]; !ok {
					t.Errorf("list_my_permissions misses %s", id)
				}
			}
			if strings.Contains(mustJSON(out)+mustJSON(perms), "camera") {
				t.Error("a list names the unreadable camera")
			}
		})
	}
}

// Walking next_cursor yields every device exactly once, in order; the last page has no
// cursor.
func TestListPages(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	for _, tool := range []string{"list_devices", "list_my_permissions"} {
		t.Run(tool, func(t *testing.T) {
			var all []string
			args := map[string]any{"limit": 3}
			for page := 1; ; page++ {
				out, errText := h.call(s, tool, args)
				if errText != "" {
					t.Fatalf("page %d: %s", page, errText)
				}
				ids := listedIDs(out)
				if tool == "list_my_permissions" {
					for id := range permissionsByDevice(out) {
						ids = append(ids, id)
					}
					slices.Sort(ids)
				}
				if len(ids) > 3 || out["total"] != float64(7) {
					t.Fatalf("page %d: %v, total %v", page, ids, out["total"])
				}
				all = append(all, ids...)
				next, _ := out["next_cursor"].(string)
				if next == "" {
					break
				}
				if page > 3 {
					t.Fatal("more pages than devices")
				}
				args = map[string]any{"limit": 3, "cursor": next}
			}
			want := []string{"alarm_control_panel.home", "climate.living_room", "light.kitchen", "lock.back_door", "lock.front_door", "lock.garden_gate", "number.wallbox"}
			if !slices.Equal(all, want) {
				t.Errorf("pages = %v, want %v", all, want)
			}
		})
	}
}

// A cursor is only a position: one naming an unreadable or missing device continues
// after it and reveals nothing.
func TestListCursorOfUnknownDevice(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	camera, errText := h.call(s, "list_devices", map[string]any{"cursor": "camera.porch"})
	if errText != "" {
		t.Fatal(errText)
	}
	missing, _ := h.call(s, "list_devices", map[string]any{"cursor": "camera.nowhere"})
	if got := listedIDs(camera); !slices.Equal(got, listedIDs(missing)) || len(got) == 0 || got[0] != "climate.living_room" {
		t.Errorf("after camera.porch %v, after camera.nowhere %v", got, listedIDs(missing))
	}
}

func TestListDefaultPageSize(t *testing.T) {
	h := newHarness(t, nil)
	h.catalog.mu.Lock()
	for i := range 520 {
		id := fmt.Sprintf("light.bulb_%03d", i)
		h.catalog.devices[id] = catalog.Device{EntityID: id, Category: "light", Areas: []string{"kitchen"}, State: "off"}
	}
	h.catalog.mu.Unlock()
	s := h.session()
	out, errText := h.call(s, "list_devices", nil)
	if errText != "" {
		t.Fatal(errText)
	}
	if n := len(listedIDs(out)); n != 100 || out["next_cursor"] != "light.bulb_097" || out["total"] != float64(527) {
		t.Errorf("default page: %d devices, next %v, total %v", n, out["next_cursor"], out["total"])
	}
	out, _ = h.call(s, "list_devices", map[string]any{"limit": 500})
	if n := len(listedIDs(out)); n != 500 {
		t.Errorf("limit 500: %d devices", n)
	}
	// Lights with identical permissions form one group.
	perms, _ := h.call(s, "list_my_permissions", map[string]any{"category": "light", "limit": 500})
	groups, _ := perms["groups"].([]any)
	if len(groups) != 1 || len(groups[0].(map[string]any)["entity_ids"].([]any)) != 500 {
		t.Errorf("groups = %s", mustJSON(groups))
	}
}

func TestListRejectsBadInput(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	for _, args := range []map[string]any{
		{"limit": -1},
		{"limit": 501},
		{"cursor": "not an entity"},
		{"cursor": strings.Repeat("a", 300)},
		{"search": strings.Repeat("a", 101)},
		{"area": strings.Repeat("a", 300)},
		{"category": strings.Repeat("a", 300)},
	} {
		for _, tool := range []string{"list_devices", "list_my_permissions"} {
			if _, errText := h.call(s, tool, args); !strings.HasPrefix(errText, "invalid_params") {
				t.Errorf("%s %v: %q, want invalid_params", tool, args, errText)
			}
		}
	}
}

// The first page of list_devices without filters tells the areas and categories of
// readable devices, with counts, so an agent can filter; later pages and filtered
// calls, whose agent knows them already, do not repeat it.
func TestListDevicesOverview(t *testing.T) {
	h := newHarness(t, nil)
	s := h.session()
	out, errText := h.call(s, "list_devices", map[string]any{"limit": 1})
	if errText != "" {
		t.Fatal(errText)
	}
	areas := mustJSON(out["areas"])
	if want := `[{"count":2,"floor":true,"id":"floor:ground","name":"Ground floor"},{"count":2,"id":"garden","name":"Garden"},{"count":1,"id":"hallway","name":"Hallway"},{"count":1,"id":"kitchen","name":"Kitchen"}]`; areas != want {
		t.Errorf("areas = %s, want %s (the porch has only the unreadable camera)", areas, want)
	}
	categories := mustJSON(out["categories"])
	if want := `[{"count":1,"name":"alarm"},{"count":1,"name":"climate"},{"count":1,"name":"light"},{"count":3,"name":"lock"},{"count":1,"name":"other"}]`; categories != want {
		t.Errorf("categories = %s, want %s", categories, want)
	}
	next, _ := h.call(s, "list_devices", map[string]any{"limit": 1, "cursor": out["next_cursor"]})
	if next["areas"] != nil || next["categories"] != nil {
		t.Errorf("overview repeated on a later page: %v", next)
	}
	for _, filter := range []map[string]any{{"category": "lock"}, {"area": "garden"}, {"search": "kitchen"}} {
		if filtered, _ := h.call(s, "list_devices", filter); filtered["areas"] != nil || filtered["categories"] != nil {
			t.Errorf("overview with %v: %v", filter, filtered)
		}
	}
}

// Every page is a request under the mandate's limit (SPEC-v0 section 11.2).
func TestListPagesCountAgainstTheRateLimit(t *testing.T) {
	h := newHarness(t, func(d map[string]any) { d["limits"] = map[string]any{"max_actions_per_hour": 2} })
	s := h.session()
	first, _ := h.call(s, "list_devices", map[string]any{"limit": 2})
	if _, errText := h.call(s, "list_devices", map[string]any{"limit": 2, "cursor": first["next_cursor"]}); errText != "" {
		t.Fatal(errText)
	}
	if _, errText := h.call(s, "list_my_permissions", map[string]any{"limit": 2}); errText != "rate_limited" {
		t.Errorf("third page: %q, want rate_limited", errText)
	}
}

// Page boundaries: a page that ends exactly at the last match has no next_cursor, and a
// cursor after it gives an empty page; a cursor continues within the filtered devices.
func TestListPageBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		args     map[string]any
		want     []string
		wantNext string
	}{
		{"limit equals total", map[string]any{"category": "lock", "limit": 3}, []string{"lock.back_door", "lock.front_door", "lock.garden_gate"}, ""},
		{"cursor at the last match", map[string]any{"category": "lock", "cursor": "lock.garden_gate"}, nil, ""},
		{"filter and cursor", map[string]any{"category": "lock", "limit": 1, "cursor": "lock.back_door"}, []string{"lock.front_door"}, "lock.front_door"},
		{"limit 0 is the default", map[string]any{"limit": 0}, []string{"alarm_control_panel.home", "climate.living_room", "light.kitchen", "lock.back_door", "lock.front_door", "lock.garden_gate", "number.wallbox"}, ""},
	}
	h := newHarness(t, nil)
	s := h.session()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, errText := h.call(s, "list_devices", tt.args)
			if errText != "" {
				t.Fatal(errText)
			}
			next, _ := out["next_cursor"].(string)
			if got := listedIDs(out); !slices.Equal(got, tt.want) || next != tt.wantNext {
				t.Errorf("list_devices = %v, next %q; want %v, next %q", got, next, tt.want, tt.wantNext)
			}
		})
	}
}

// Devices with different permissions form separate groups; a device whose read is ask
// is in list_my_permissions but not in list_devices.
func TestListMyPermissionsGroups(t *testing.T) {
	h := newHarness(t, func(d map[string]any) {
		rules := d["rules"].([]any)
		d["rules"] = append(rules, map[string]any{"id": "r-gate-read", "resource": map[string]any{"entity_id": "lock.garden_gate"}, "actions": []any{"read"}, "decision": "ask"})
	})
	s := h.session()
	out, errText := h.call(s, "list_my_permissions", map[string]any{"category": "lock"})
	if errText != "" {
		t.Fatal(errText)
	}
	want := `[{"actions":{"open":"ask","read":"allow","unlock":"ask"},"category":"lock","entity_ids":["lock.back_door","lock.front_door"]},` +
		`{"actions":{"open":"ask","read":"ask","unlock":"ask"},"category":"lock","entity_ids":["lock.garden_gate"]}]`
	if got := mustJSON(out["groups"]); got != want {
		t.Errorf("groups = %s, want %s", got, want)
	}
	devices, _ := h.call(s, "list_devices", map[string]any{"category": "lock"})
	if got := listedIDs(devices); !slices.Equal(got, []string{"lock.back_door", "lock.front_door"}) {
		t.Errorf("list_devices = %v; the gate's read is ask", got)
	}
}
