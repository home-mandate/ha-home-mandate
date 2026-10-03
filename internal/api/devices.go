// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"github.com/home-mandate/home-mandate/internal/mcp"
)

type wireArea struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type wireDevice struct {
	EntityID string   `json:"entity_id"`
	Name     string   `json:"name"`
	Category string   `json:"category"`
	Area     *string  `json:"area"`
	Actions  []string `json:"actions"`
}

type wireDeviceCatalog struct {
	Areas   []wireArea   `json:"areas"`
	Devices []wireDevice `json:"devices"`
}

// getDevices is the catalog for the mandate editor: every device with its category,
// area and the actions of its category's vocabulary. Names come from Home Assistant and
// are untrusted; the UI shows them cleaned and escaped.
func (s *Server) getDevices(*request) (any, error) {
	out := wireDeviceCatalog{Areas: []wireArea{}, Devices: []wireDevice{}}
	for _, a := range s.cfg.Catalog.Areas() {
		out.Areas = append(out.Areas, wireArea{ID: a.ID, Name: a.Name})
	}
	for _, d := range s.cfg.Catalog.All() {
		name := d.Name()
		if name == "" {
			name = d.EntityID
		}
		actions := mcp.Actions(d.Category)
		if actions == nil {
			actions = []string{}
		}
		out.Devices = append(out.Devices, wireDevice{EntityID: d.EntityID, Name: name, Category: d.Category, Area: optional(d.Area),
			Actions: actions})
	}
	return out, nil
}
