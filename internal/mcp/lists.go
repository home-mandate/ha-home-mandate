// SPDX-License-Identifier: AGPL-3.0-or-later

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/home-mandate/spec/evaluator"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/pdp"
)

// Pages of list_devices and list_my_permissions (issue #36): an installation with
// 1734 entities answered with about 250 KB, more than clients accept as one result.
const (
	defaultPageSize = 100
	maxPageSize     = 500
	// maxSearchRunes and maxAreaBytes bound the filters.
	maxSearchRunes = 100
	maxAreaBytes   = 255
)

var categoryPattern = regexp.MustCompile(`^[a-z_]{1,64}$`)

type (
	// listInput filters and pages both lists. Filters apply only to the devices the
	// agent may read, so an unknown value and one that matches only unreadable devices
	// give the same empty page.
	listInput struct {
		Area     string `json:"area,omitempty" jsonschema:"only devices in this area or on this floor, by its id from the areas of list_devices"`
		Category string `json:"category,omitempty" jsonschema:"only devices of this category, e.g. light or lock"`
		Search   string `json:"search,omitempty" jsonschema:"only devices whose entity ID or name contains this text, ignoring case"`
		Cursor   string `json:"cursor,omitempty" jsonschema:"next_cursor of the previous page; omit it for the first page"`
		Limit    int    `json:"limit,omitempty" jsonschema:"devices per page, 1 to 500; default 100"`
	}
	deviceOut struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name,omitempty"`
		Category string `json:"category"`
		// Areas are the room and the floor of the device; IDs of floors begin with floor:.
		Areas []string `json:"areas,omitempty"`
		State string   `json:"state"`
	}
	areaOut struct {
		ID   string `json:"id"`
		Name string `json:"name,omitempty"`
		// Floor: a floor of the home; its devices are those of the rooms on it.
		Floor bool `json:"floor,omitempty"`
		Count int  `json:"count"`
	}
	categoryOut struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	devicesOut struct {
		Devices []deviceOut `json:"devices"`
		// Total is the number of devices that match the filters, on all pages.
		Total int `json:"total"`
		// NextCursor continues with the next page; it is missing on the last one.
		NextCursor string `json:"next_cursor,omitempty"`
		// Areas and Categories cover every device the agent may read, on the first page
		// without filters only: an agent that filters knows them already.
		Areas      []areaOut     `json:"areas,omitempty"`
		Categories []categoryOut `json:"categories,omitempty"`
	}
	// permissionGroup is the devices of one category with the same permissions.
	permissionGroup struct {
		Category  string            `json:"category"`
		Actions   map[string]string `json:"actions"`
		EntityIDs []string          `json:"entity_ids"`
		// Limits are the ranges an allowed action is limited to, per action: one entry per
		// rule that could allow it, in the agent's units.
		Limits map[string][]map[string]limitOut `json:"limits,omitempty"`
	}
	permissionsOut struct {
		Groups     []permissionGroup `json:"groups"`
		Total      int               `json:"total"`
		NextCursor string            `json:"next_cursor,omitempty"`
	}
)

// pageSize checks the input and returns the number of devices per page.
func (in listInput) pageSize() (int, error) {
	invalid := func(field string) error { return errors.New(codeInvalidParams + ": " + field) }
	switch {
	case in.Limit < 0 || in.Limit > maxPageSize:
		return 0, invalid(fmt.Sprintf("limit must be between 1 and %d", maxPageSize))
	case in.Cursor != "" && !entityIDPattern.MatchString(in.Cursor):
		return 0, invalid("cursor")
	case in.Category != "" && !categoryPattern.MatchString(in.Category):
		return 0, invalid("category")
	case len(in.Area) > maxAreaBytes || !utf8.ValidString(in.Area):
		return 0, invalid("area")
	case utf8.RuneCountInString(in.Search) > maxSearchRunes || !utf8.ValidString(in.Search):
		return 0, invalid(fmt.Sprintf("search must be at most %d characters", maxSearchRunes))
	case in.Limit == 0:
		return defaultPageSize, nil
	}
	return in.Limit, nil
}

// matches reports whether dev passes the filters; search is in.Search in lower case.
func (in listInput) matches(dev catalog.Device, search string) bool {
	if in.Area != "" && !slices.Contains(dev.Areas, in.Area) {
		return false
	}
	if in.Category != "" && dev.Category != in.Category {
		return false
	}
	if search == "" {
		return true
	}
	return strings.Contains(strings.ToLower(dev.EntityID), search) || strings.Contains(strings.ToLower(dev.Name()), search)
}

// page returns the devices that match the filters and come after the cursor, at most
// size of them; the number of matches on all pages; and the cursor of the next page.
// The cursor is a position in the order of entity IDs, so one naming a device the
// agent may not read reveals nothing.
func (in listInput) page(devices []catalog.Device, size int) (page []catalog.Device, total int, next string) {
	search := strings.ToLower(in.Search)
	for _, dev := range devices {
		if !in.matches(dev, search) {
			continue
		}
		total++
		if in.Cursor != "" && dev.EntityID <= in.Cursor {
			continue
		}
		if len(page) == size {
			next = page[len(page)-1].EntityID
			continue
		}
		page = append(page, dev)
	}
	return page, total, next
}

// readableDevices returns, in the order of entity IDs, the devices whose read is
// allowed (allowedOnly) or not denied.
func (g *Gateway) readableDevices(snap *pdp.Snapshot, allowedOnly bool) []catalog.Device {
	var out []catalog.Device
	for _, dev := range g.cfg.Catalog.All() {
		d := snap.Decide(dev.EntityID, "read", nil).Result.Decision
		if d == evaluator.Allow || (!allowedOnly && d != evaluator.Deny) {
			out = append(out, dev)
		}
	}
	// The catalog sorts already; pages depend on the order, so it is not taken on trust.
	slices.SortFunc(out, func(a, b catalog.Device) int { return strings.Compare(a.EntityID, b.EntityID) })
	return out
}

func (g *Gateway) listDevices(ctx context.Context, req *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, devicesOut, error) {
	out := devicesOut{Devices: []deviceOut{}}
	size, err := in.pageSize()
	if err != nil {
		return nil, out, err
	}
	snap, err := g.listSnapshot(ctx, req)
	if err != nil {
		return nil, out, err
	}
	// Only devices the agent may read now; ask would need a human first.
	readable := g.readableDevices(snap, true)
	page, total, next := in.page(readable, size)
	for _, dev := range page {
		out.Devices = append(out.Devices, deviceOut{EntityID: dev.EntityID, Name: dev.Name(), Category: dev.Category, Areas: dev.Areas, State: dev.State})
	}
	out.Total, out.NextCursor = total, next
	if in.Cursor == "" && in.Area == "" && in.Category == "" && in.Search == "" {
		out.Areas, out.Categories = g.overview(readable)
	}
	return nil, out, nil
}

// overview counts the devices per area (rooms and floors) and per category. It names only
// areas with a device the agent may read.
func (g *Gateway) overview(devices []catalog.Device) ([]areaOut, []categoryOut) {
	areaCount, categoryCount := map[string]int{}, map[string]int{}
	for _, dev := range devices {
		for _, area := range dev.Areas {
			areaCount[area]++
		}
		categoryCount[dev.Category]++
	}
	names, floors := map[string]string{}, map[string]bool{}
	for _, a := range g.cfg.Catalog.Areas() {
		names[a.ID], floors[a.ID] = a.Name, a.Floor
	}
	areas := []areaOut{}
	for _, id := range slices.Sorted(maps.Keys(areaCount)) {
		areas = append(areas, areaOut{ID: id, Name: names[id], Floor: floors[id], Count: areaCount[id]})
	}
	categories := []categoryOut{}
	for _, name := range slices.Sorted(maps.Keys(categoryCount)) {
		categories = append(categories, categoryOut{Name: name, Count: categoryCount[name]})
	}
	return areas, categories
}

func (g *Gateway) listPermissions(ctx context.Context, req *sdk.CallToolRequest, in listInput) (*sdk.CallToolResult, permissionsOut, error) {
	out := permissionsOut{Groups: []permissionGroup{}}
	size, err := in.pageSize()
	if err != nil {
		return nil, out, err
	}
	snap, err := g.listSnapshot(ctx, req)
	if err != nil {
		return nil, out, err
	}
	// Devices the agent may not read are left out even when the mandate allows other
	// actions on them. Every other device has a permission: its read, allow or ask.
	page, total, next := in.page(g.readableDevices(snap, false), size)
	unit := g.temperatureUnit()
	index := map[string]int{} // groupKey → position in out.Groups
	for _, dev := range page {
		actions := map[string]string{}
		var limits map[string][]map[string]limitOut
		for _, action := range actionsOf(dev.Category) {
			d := snap.Decide(dev.EntityID, action, nil)
			if d.Result.Decision != evaluator.Deny {
				actions[action] = string(d.Result.Decision)
				continue
			}
			// Evaluated without values, an action allowed only within limits does not
			// match: it is allowed with the values its limits name.
			if d.Result.Reason != evaluator.ReasonNoMatch {
				continue
			}
			lims := agentLimits(snap.Limits(dev.EntityID, action), dev.Category, action, unit)
			if len(lims) == 0 {
				continue
			}
			actions[action] = string(evaluator.Allow)
			if !unlimited(lims) {
				if limits == nil {
					limits = map[string][]map[string]limitOut{}
				}
				limits[action] = lims
			}
		}
		key := groupKey(dev.Category, actions, limits)
		i, ok := index[key]
		if !ok {
			i = len(out.Groups)
			index[key] = i
			out.Groups = append(out.Groups, permissionGroup{Category: dev.Category, Actions: actions, Limits: limits})
		}
		out.Groups[i].EntityIDs = append(out.Groups[i].EntityIDs, dev.EntityID)
	}
	out.Total, out.NextCursor = total, next
	return nil, out, nil
}

// groupKey identifies a category with its permissions and limits; categories, actions
// and decisions consist of [a-z_], so the separators cannot occur in them, and the limits
// follow as JSON (maps encode in key order).
func groupKey(category string, actions map[string]string, limits map[string][]map[string]limitOut) string {
	var b strings.Builder
	b.WriteString(category)
	for _, action := range slices.Sorted(maps.Keys(actions)) {
		b.WriteString(";" + action + "=" + actions[action])
	}
	if limits != nil {
		encoded, _ := json.Marshal(limits)
		b.WriteString("|")
		b.Write(encoded)
	}
	return b.String()
}
