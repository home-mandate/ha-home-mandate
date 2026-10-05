// SPDX-License-Identifier: AGPL-3.0-or-later

// Package catalog maps Home Assistant entities to the vocabulary of SPEC-v0 section 5:
// category and area of every device. The PEP takes both from here, never from the agent.
// Until the first successful refresh the catalog is empty, so every request is denied.
package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// Source is what the catalog reads from Home Assistant.
type Source interface {
	GetStates(ctx context.Context) ([]ha.State, error)
	ListEntities(ctx context.Context) ([]ha.EntityEntry, error)
	ListDevices(ctx context.Context) ([]ha.Device, error)
	ListAreas(ctx context.Context) ([]ha.Area, error)
}

// Area is an area with its name, for the UI.
type Area struct {
	ID   string
	Name string
}

// Device is an entity as the PEP sees it.
type Device struct {
	EntityID string
	Category string
	Area     string
	// Critical is true if the household marked the entity as critical: every action on
	// it except read then needs a confirmation or allow_critical (SPEC-v0 section 4).
	Critical bool
	// Formers are former IDs of a renamed entity that a human has not resolved yet: rules
	// on them keep applying to it, the stricter evaluation wins.
	Formers    []string
	State      string
	Attributes map[string]any
}

// domainCategory maps entity domains to categories (SPEC-v0 section 5, column Home
// Assistant). cover is handled separately; everything else is "other".
var domainCategory = map[string]string{
	"light":               "light",
	"switch":              "switch",
	"climate":             "climate",
	"lock":                "lock",
	"alarm_control_panel": "alarm",
	"camera":              "camera",
	"media_player":        "media",
	"sensor":              "sensor",
	"binary_sensor":       "sensor",
	"scene":               "scene",
	"script":              "script",
}

// Category returns the category of an entity from its domain and device_class.
func Category(entityID, deviceClass string) string {
	domain, _, _ := strings.Cut(entityID, ".")
	if domain == "cover" {
		if deviceClass == "garage" || deviceClass == "gate" {
			return "gate"
		}
		return "cover"
	}
	if c, ok := domainCategory[domain]; ok {
		return c
	}
	return "other"
}

// Catalog is a snapshot of the household's devices, kept current by events.
type Catalog struct {
	src Source
	log *slog.Logger

	mu         sync.RWMutex
	ready      bool
	devices    map[string]Device
	entityArea map[string]string // entity → area from the registries
	areas      []Area            // sorted by ID
	disabled   map[string]bool
	// marks are the entities the household marked as critical; nil: none.
	marks interface{ Critical(entityID string) bool }
	// While a refresh fetches, state changes are also kept here and applied on top of
	// the new snapshot, so that an older snapshot never overwrites a newer event.
	refreshing bool
	pending    []stateChange
	// renames are the entity IDs Home Assistant renamed since the last refresh by Run.
	renames []Rename
	// aliases keeps renames until a human resolves them; nil: renames are only reported.
	aliases   Aliases
	onRefresh func([]Rename)

	refresh chan struct{}
}

// Rename is an entity ID that Home Assistant changed. Rules and marks name entities by
// ID, so they no longer apply to the renamed entity (decision H-E1: report, never rewrite).
type Rename struct {
	Old string
	New string
	// Registry is the registry ID of Home Assistant the rename was found by; empty for
	// one from the rename event.
	Registry string
}

// maxRenames bounds the renames kept between two refreshes.
const maxRenames = 1000

// OnRefresh sets fn to run after every successful refresh by Run, with the renames that
// refresh covers (possibly none: the directory may have changed in other ways). fn runs
// on Run's goroutine and may block it; call OnRefresh before Run.
func (c *Catalog) OnRefresh(fn func(renames []Rename)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.onRefresh = fn
}

// New returns an empty catalog; call Refresh or Run to load it.
func New(src Source, log *slog.Logger) *Catalog {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Catalog{src: src, log: log, devices: map[string]Device{}, refresh: make(chan struct{}, 1)}
}

// Ready reports whether the catalog was loaded at least once.
func (c *Catalog) Ready() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	// Renames that can no longer be held would be forgotten: nothing is decided then.
	return c.ready && (c.aliases == nil || !c.aliases.Overflowing())
}

// Lookup returns a copy of the device with entityID.
func (c *Catalog) Lookup(entityID string) (Device, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	d, ok := c.devices[entityID]
	if !ok {
		return Device{}, false
	}
	return c.marked(clone(d)), true
}

// Aliases are the renames a human has not resolved yet (Renames).
type Aliases interface {
	Hold(Rename) bool
	Observe([]ha.EntityEntry) []Rename
	Formers(entityID string) []string
	// Overflowing: more renames wait to be stored than can be held.
	Overflowing() bool
}

// SetAliases sets where renames are kept until a human resolves them.
func (c *Catalog) SetAliases(a Aliases) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.aliases = a
}

// SetMarks tells the catalog which entities the household marked as critical; without
// it no entity is.
func (c *Catalog) SetMarks(marks interface{ Critical(entityID string) bool }) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.marks = marks
}

// marked sets Critical from the marks of the household and Formers from the unresolved
// renames; the caller holds the lock.
func (c *Catalog) marked(d Device) Device {
	d.Critical = c.marks != nil && c.marks.Critical(d.EntityID)
	if c.aliases != nil {
		d.Formers = c.aliases.Formers(d.EntityID)
	}
	return d
}

// All returns copies of all devices, sorted by entity ID.
func (c *Catalog) All() []Device {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]Device, 0, len(c.devices))
	for _, id := range slices.Sorted(maps.Keys(c.devices)) {
		out = append(out, c.marked(clone(c.devices[id])))
	}
	return out
}

// Areas returns the areas of Home Assistant, sorted by ID.
func (c *Catalog) Areas() []Area {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return slices.Clone(c.areas)
}

// Name is the device's friendly_name from Home Assistant, untrusted; empty if it has none.
func (d Device) Name() string {
	name, _ := d.Attributes["friendly_name"].(string)
	return name
}

func clone(d Device) Device {
	d.Attributes = maps.Clone(d.Attributes)
	return d
}

type stateChange struct {
	entityID string
	state    *ha.State // nil: removed
}

// maxPending bounds the events kept during one refresh.
const maxPending = 10000

// Invalidate marks the catalog as not ready, e.g. after the connection to Home
// Assistant was lost and events may be missing; the next refresh makes it ready again.
func (c *Catalog) Invalidate() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ready = false
}

// Refresh reloads states and registries. On failure the last snapshot stays.
func (c *Catalog) Refresh(ctx context.Context) error {
	c.mu.Lock()
	c.refreshing, c.pending = true, nil
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.refreshing, c.pending = false, nil
		c.mu.Unlock()
	}()
	states, err := c.src.GetStates(ctx)
	if err != nil {
		return fmt.Errorf("catalog: states: %w", err)
	}
	entities, err := c.src.ListEntities(ctx)
	if err != nil {
		return fmt.Errorf("catalog: entity registry: %w", err)
	}
	devices, err := c.src.ListDevices(ctx)
	if err != nil {
		return fmt.Errorf("catalog: device registry: %w", err)
	}
	areaList, err := c.src.ListAreas(ctx)
	if err != nil {
		return fmt.Errorf("catalog: area registry: %w", err)
	}
	areas := make([]Area, 0, len(areaList))
	for _, a := range areaList {
		if a.AreaID != "" {
			areas = append(areas, Area{ID: a.AreaID, Name: a.Name})
		}
	}
	slices.SortFunc(areas, func(a, b Area) int { return strings.Compare(a.ID, b.ID) })

	deviceArea := make(map[string]string, len(devices))
	for _, d := range devices {
		deviceArea[d.ID] = d.AreaID
	}
	entityArea := make(map[string]string, len(entities))
	disabled := map[string]bool{}
	for _, e := range entities {
		if e.DisabledBy != "" {
			disabled[e.EntityID] = true
		}
		area := e.AreaID
		if area == "" {
			area = deviceArea[e.DeviceID]
		}
		entityArea[e.EntityID] = area
	}
	snapshot := make(map[string]Device, len(states))
	for _, s := range states {
		if disabled[s.EntityID] {
			continue
		}
		snapshot[s.EntityID] = device(s, entityArea[s.EntityID])
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pending) >= maxPending {
		return fmt.Errorf("catalog: too many changes during refresh")
	}
	// Renames while the events were missed (an outage, a restart) are found by the
	// registry IDs, and take effect before the new IDs become visible.
	if c.aliases != nil {
		for _, rn := range c.aliases.Observe(entities) {
			c.holdLocked(rn)
		}
	}
	c.devices, c.entityArea, c.disabled, c.areas = snapshot, entityArea, disabled, areas
	for _, ch := range c.pending {
		c.applyLocked(ch)
	}
	c.ready = true
	return nil
}

func device(s ha.State, area string) Device {
	deviceClass, _ := s.Attributes["device_class"].(string)
	return Device{EntityID: s.EntityID, Category: Category(s.EntityID, deviceClass), Area: area,
		State: s.State, Attributes: maps.Clone(s.Attributes)}
}

// HandleEvent applies a state_changed event or schedules a refresh after a registry
// change. It runs on the Home Assistant read loop and never blocks.
func (c *Catalog) HandleEvent(e ha.Event) {
	if strings.HasSuffix(e.EventType, "_registry_updated") {
		if e.EventType == "entity_registry_updated" {
			c.noteRename(e.Data)
		}
		c.RequestRefresh()
		return
	}
	if e.EventType != ha.EventStateChanged {
		return
	}
	var data struct {
		EntityID string    `json:"entity_id"`
		NewState *ha.State `json:"new_state"`
	}
	if err := json.Unmarshal(e.Data, &data); err != nil || data.NewState != nil && data.NewState.EntityID != data.EntityID {
		c.log.Warn("ignored malformed state_changed event")
		return
	}
	ch := stateChange{entityID: data.EntityID, state: data.NewState}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refreshing && len(c.pending) < maxPending {
		c.pending = append(c.pending, ch)
	}
	c.applyLocked(ch)
}

// noteRename keeps a rename from an entity_registry_updated event; anything else in the
// event is ignored, the refresh reads the registries anew.
func (c *Catalog) noteRename(data json.RawMessage) {
	var d struct {
		Action      string `json:"action"`
		EntityID    any    `json:"entity_id"`
		OldEntityID any    `json:"old_entity_id"`
	}
	if json.Unmarshal(data, &d) != nil || d.Action != "update" {
		return
	}
	newID, ok1 := d.EntityID.(string)
	oldID, ok2 := d.OldEntityID.(string)
	if !ok1 || !ok2 || !opaque(newID) || !opaque(oldID) || newID == oldID {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.holdLocked(Rename{Old: oldID, New: newID})
}

// holdLocked takes a rename into effect at once, before the new ID can be decided on:
// rules on the old ID keep applying and the critical mark moves along. Run stores both
// after the refresh. The caller holds the lock.
func (c *Catalog) holdLocked(rn Rename) {
	if h, ok := c.marks.(interface {
		Hold(oldID, newID string) bool
	}); ok && h.Hold(rn.Old, rn.New) {
		c.log.Warn("critical mark held for the renamed entity", "old", rn.Old, "new", rn.New)
	}
	if c.aliases != nil {
		c.aliases.Hold(rn)
	}
	if slices.ContainsFunc(c.renames, func(x Rename) bool { return x.Old == rn.Old && x.New == rn.New }) {
		return
	}
	if len(c.renames) >= maxRenames {
		c.log.Error("rename not reported: too many renames since the last refresh", "old", rn.Old, "new", rn.New)
		return
	}
	c.renames = append(c.renames, rn)
}

func (c *Catalog) applyLocked(ch stateChange) {
	if c.disabled[ch.entityID] {
		return
	}
	if ch.state == nil {
		delete(c.devices, ch.entityID)
		return
	}
	c.devices[ch.entityID] = device(*ch.state, c.entityArea[ch.entityID])
}

// RequestRefresh schedules a refresh by Run; it never blocks.
func (c *Catalog) RequestRefresh() {
	select {
	case c.refresh <- struct{}{}:
	default: // one is already pending
	}
}

// Run performs requested refreshes until ctx ends. It waits debounce after a request so
// that a burst of registry events causes one refresh, and retries failed refreshes.
func (c *Catalog) Run(ctx context.Context, debounce time.Duration) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-c.refresh:
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(debounce):
		}
		select { // requests that arrived during the wait are covered by this refresh
		case <-c.refresh:
		default:
		}
		if err := c.Refresh(ctx); err != nil {
			// The renames stay and are reported after the refresh that succeeds.
			if ctx.Err() == nil {
				c.log.Warn("catalog refresh failed, retrying", "error", err)
				c.RequestRefresh()
			}
			continue
		}
		c.mu.Lock()
		renames, fn := c.renames, c.onRefresh
		c.renames = nil
		c.mu.Unlock()
		if fn != nil {
			fn(renames)
		}
	}
}
