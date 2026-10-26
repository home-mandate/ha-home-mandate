// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"github.com/home-mandate/ha-home-mandate/internal/catalog"
	"github.com/home-mandate/ha-home-mandate/internal/mcp"
)

// wireArea is an area of Home Assistant or a floor; a rule can name either.
type wireArea struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Floor: the area is a floor of Home Assistant (its ID begins with floor:).
	Floor bool `json:"floor"`
	// FloorID is the area ID of the floor an area is on.
	FloorID *string `json:"floor_id"`
}

type wireDevice struct {
	EntityID string `json:"entity_id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	// Area is the room of the device, Areas its set of areas: the room and its floor.
	Area    *string  `json:"area"`
	Areas   []string `json:"areas"`
	Actions []string `json:"actions"`
	// Critical: the household marked the device; every action except read is critical.
	Critical bool `json:"critical"`
	// SuggestCritical: the UI proposes marking the device (catalog.SuggestCritical).
	SuggestCritical bool `json:"suggest_critical"`
}

type wireDeviceCatalog struct {
	Areas   []wireArea   `json:"areas"`
	Devices []wireDevice `json:"devices"`
}

// getDevices is the catalog for the mandate editor: every device with its category,
// areas and the actions of its category's vocabulary. Names come from Home Assistant and
// are untrusted; the UI shows them cleaned and escaped.
func (s *Server) getDevices(*request) (any, error) {
	out := wireDeviceCatalog{Areas: []wireArea{}, Devices: []wireDevice{}}
	for _, a := range s.cfg.Catalog.Areas() {
		out.Areas = append(out.Areas, wireArea{ID: a.ID, Name: a.Name, Floor: a.Floor, FloorID: optional(a.FloorID)})
	}
	for _, d := range s.cfg.Catalog.All() {
		name := d.Name()
		if name == "" {
			name = d.EntityID
		}
		// The whole vocabulary, not only what the device reports it can do now (issue #89):
		// the editor's warnings and its analysis of a rule's effect must cover what a rule
		// permits once the device can do it, also after an integration changes its features.
		actions := mcp.Actions(d.Category)
		if actions == nil {
			actions = []string{}
		}
		areas := d.Areas
		if areas == nil {
			areas = []string{}
		}
		out.Devices = append(out.Devices, wireDevice{EntityID: d.EntityID, Name: name, Category: d.Category,
			Area: optional(catalog.Room(d.Areas)), Areas: areas,
			Actions: actions, Critical: d.Critical, SuggestCritical: !d.Critical && catalog.SuggestCritical(d)})
	}
	return out, nil
}

// putDeviceCritical marks a device as critical or removes the mark (SPEC-v0 section 4,
// step 5). Removing a mark lowers the protection of the device, so both are logged.
func (s *Server) putDeviceCritical(r *request) (any, error) {
	var in struct {
		EntityID *string `json:"entity_id"`
		Critical *bool   `json:"critical"`
	}
	if err := r.decode(&in); err != nil {
		return nil, err
	}
	if in.EntityID == nil {
		return nil, failField(codeInvalidInput, "/entity_id")
	}
	if in.Critical == nil {
		return nil, failField(codeInvalidInput, "/critical")
	}
	device, ok := s.cfg.Catalog.Lookup(*in.EntityID)
	if !ok {
		return nil, fail(codeNotFound)
	}
	// Marks.Set writes the change with its directory.changed audit entry; the server log
	// keeps failed attempts too.
	if err := s.cfg.Marks.Set(r.Context(), *in.EntityID, *in.Critical, r.user); err != nil {
		s.cfg.Logger.Error("device critical mark not changed", "entity_id", *in.EntityID, "critical", *in.Critical, "by", r.user, "error", err)
		return nil, err
	}
	s.cfg.Logger.Warn("device critical mark changed", "entity_id", *in.EntityID, "critical", *in.Critical, "was", device.Critical, "by", r.user)
	s.publish(event{Type: "devices.changed"})
	return nil, nil
}
