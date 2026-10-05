// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/home-mandate/home-mandate/internal/ha"
)

// fakeSource is a scriptable Home Assistant.
type fakeSource struct {
	mu       sync.Mutex
	states   []ha.State
	entities []ha.EntityEntry
	devices  []ha.Device
	areas    []ha.Area
	err      error
	areaErr  error
	calls    int
}

func (f *fakeSource) ListAreas(context.Context) ([]ha.Area, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.areas, f.areaErr
}

func (f *fakeSource) GetStates(context.Context) ([]ha.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.states, f.err
}

func (f *fakeSource) ListEntities(context.Context) ([]ha.EntityEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.entities, f.err
}

func (f *fakeSource) ListDevices(context.Context) ([]ha.Device, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.devices, f.err
}

func (f *fakeSource) set(fn func(*fakeSource)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

func (f *fakeSource) refreshes() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func state(id, s string, attrs map[string]any) ha.State {
	return ha.State{EntityID: id, State: s, Attributes: attrs}
}

func house() *fakeSource {
	return &fakeSource{
		states: []ha.State{
			state("light.kitchen", "off", map[string]any{"friendly_name": "Kitchen"}),
			state("switch.pool_pump", "on", nil),
			state("climate.living_room", "heat", nil),
			state("cover.living_room_blinds", "open", map[string]any{"device_class": "blind"}),
			state("cover.garage_door", "closed", map[string]any{"device_class": "garage"}),
			state("cover.driveway", "closed", map[string]any{"device_class": "gate"}),
			state("lock.front_door", "locked", nil),
			state("alarm_control_panel.home", "armed_away", nil),
			state("camera.porch", "idle", nil),
			state("media_player.tv", "off", nil),
			state("sensor.temperature", "21.5", map[string]any{"unit_of_measurement": "°C"}),
			state("binary_sensor.window", "off", nil),
			state("scene.movie", "scening", nil),
			state("script.good_night", "off", nil),
			state("number.wallbox_current", "16", nil),
			state("person.anna", "home", map[string]any{"user_id": "u1"}),
		},
		entities: []ha.EntityEntry{
			{EntityID: "light.kitchen", AreaID: "kitchen"},
			{EntityID: "lock.front_door", DeviceID: "d-lock"},
			{EntityID: "camera.porch", DeviceID: "d-cam", AreaID: "porch"},
			{EntityID: "switch.disabled", DisabledBy: "user"},
		},
		devices: []ha.Device{
			{ID: "d-lock", AreaID: "hallway"},
			{ID: "d-cam", AreaID: "garden"},
		},
	}
}

func loaded(t *testing.T, src *fakeSource) *Catalog {
	t.Helper()
	c := New(src, nil)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCategoriesFollowTheVocabulary(t *testing.T) {
	c := loaded(t, house())
	want := map[string]string{
		"light.kitchen":            "light",
		"switch.pool_pump":         "switch",
		"climate.living_room":      "climate",
		"cover.living_room_blinds": "cover",
		"cover.garage_door":        "gate",
		"cover.driveway":           "gate",
		"lock.front_door":          "lock",
		"alarm_control_panel.home": "alarm",
		"camera.porch":             "camera",
		"media_player.tv":          "media",
		"sensor.temperature":       "sensor",
		"binary_sensor.window":     "sensor",
		"scene.movie":              "scene",
		"script.good_night":        "script",
		"number.wallbox_current":   "other",
		"person.anna":              "other",
	}
	for id, category := range want {
		d, ok := c.Lookup(id)
		if !ok || d.Category != category {
			t.Errorf("%s: %+v, %v; want category %s", id, d, ok, category)
		}
	}
	if len(c.All()) != len(want) {
		t.Errorf("All() = %d devices, want %d", len(c.All()), len(want))
	}
}

func TestAreaComesFromEntityThenDevice(t *testing.T) {
	c := loaded(t, house())
	for id, area := range map[string]string{
		"light.kitchen":    "kitchen", // entity area
		"lock.front_door":  "hallway", // device area
		"camera.porch":     "porch",   // entity area wins over device area
		"switch.pool_pump": "",        // none
	} {
		if d, _ := c.Lookup(id); d.Area != area {
			t.Errorf("%s: area %q, want %q", id, d.Area, area)
		}
	}
}

func TestDisabledAndUnknownEntitiesAreNotFound(t *testing.T) {
	src := house()
	src.states = append(src.states, state("switch.disabled", "off", nil))
	c := loaded(t, src)
	for _, id := range []string{"switch.disabled", "light.nowhere", "", "LIGHT.KITCHEN"} {
		if d, ok := c.Lookup(id); ok {
			t.Errorf("Lookup(%q) = %+v", id, d)
		}
	}
}

func TestNotReadyBeforeTheFirstRefresh(t *testing.T) {
	src := house()
	c := New(src, nil)
	if c.Ready() {
		t.Error("ready before refresh")
	}
	if _, ok := c.Lookup("light.kitchen"); ok {
		t.Error("lookup succeeded before refresh")
	}
	src.set(func(f *fakeSource) { f.err = errors.New("not connected") })
	if err := c.Refresh(context.Background()); err == nil || c.Ready() {
		t.Errorf("failed refresh: err %v, ready %v", err, c.Ready())
	}
}

func TestFailedRefreshKeepsTheLastSnapshot(t *testing.T) {
	src := house()
	c := loaded(t, src)
	src.set(func(f *fakeSource) { f.err = errors.New("not connected") })
	if err := c.Refresh(context.Background()); err == nil {
		t.Fatal("refresh succeeded")
	}
	if _, ok := c.Lookup("light.kitchen"); !ok {
		t.Error("snapshot lost after a failed refresh")
	}
}

func event(t *testing.T, typ string, data any) ha.Event {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	return ha.Event{EventType: typ, Data: raw}
}

func TestStateChangedUpdatesStateOnly(t *testing.T) {
	c := loaded(t, house())
	c.HandleEvent(event(t, "state_changed", map[string]any{
		"entity_id": "light.kitchen",
		"new_state": map[string]any{"entity_id": "light.kitchen", "state": "on", "attributes": map[string]any{"brightness": 200}},
	}))
	d, _ := c.Lookup("light.kitchen")
	if d.State != "on" || d.Attributes["brightness"] != 200.0 || d.Area != "kitchen" || d.Category != "light" {
		t.Errorf("after state_changed: %+v", d)
	}
	// A removed entity (new_state null) disappears.
	c.HandleEvent(event(t, "state_changed", map[string]any{"entity_id": "light.kitchen", "new_state": nil}))
	if _, ok := c.Lookup("light.kitchen"); ok {
		t.Error("removed entity still found")
	}
	// A new entity appears with its category.
	c.HandleEvent(event(t, "state_changed", map[string]any{
		"entity_id": "lock.back_door",
		"new_state": map[string]any{"entity_id": "lock.back_door", "state": "locked"},
	}))
	if d, ok := c.Lookup("lock.back_door"); !ok || d.Category != "lock" {
		t.Errorf("new entity: %+v, %v", d, ok)
	}
	// Malformed events and events for disabled entities change nothing.
	c.HandleEvent(ha.Event{EventType: "state_changed", Data: json.RawMessage(`{`)})
	c.HandleEvent(event(t, "state_changed", map[string]any{"entity_id": "x", "new_state": map[string]any{"entity_id": "other.y", "state": "1"}}))
	if _, ok := c.Lookup("other.y"); ok {
		t.Error("event with mismatching entity_id was applied")
	}
	c.HandleEvent(event(t, "state_changed", map[string]any{
		"entity_id": "switch.disabled", "new_state": map[string]any{"entity_id": "switch.disabled", "state": "on"},
	}))
	if _, ok := c.Lookup("switch.disabled"); ok {
		t.Error("disabled entity appeared through an event")
	}
}

func TestRegistryUpdatesTriggerARefresh(t *testing.T) {
	src := house()
	c := New(src, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		c.Run(ctx, 5*time.Millisecond)
		close(done)
	}()
	c.RequestRefresh()
	waitFor(t, "first refresh", func() bool { return c.Ready() })

	src.set(func(f *fakeSource) {
		f.entities = append(f.entities, ha.EntityEntry{EntityID: "switch.pool_pump", AreaID: "garden"})
	})
	before := src.refreshes()
	for range 5 { // a burst is coalesced into few refreshes
		c.HandleEvent(event(t, "entity_registry_updated", map[string]any{"action": "update", "entity_id": "switch.pool_pump"}))
	}
	waitFor(t, "refresh after registry update", func() bool {
		d, _ := c.Lookup("switch.pool_pump")
		return d.Area == "garden"
	})
	if n := src.refreshes() - before; n > 2 {
		t.Errorf("%d refreshes for one burst, want at most 2", n)
	}
	cancel()
	<-done
}

func TestRetriesAFailedRefresh(t *testing.T) {
	src := house()
	src.err = errors.New("not connected")
	c := New(src, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, 5*time.Millisecond)
	c.RequestRefresh()
	waitFor(t, "a failed attempt", func() bool { return src.refreshes() >= 1 })
	src.set(func(f *fakeSource) { f.err = nil })
	waitFor(t, "retry", func() bool { return c.Ready() })
}

func TestConcurrentLookupsAndEvents(t *testing.T) {
	c := loaded(t, house())
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			for range 200 {
				if i%2 == 0 {
					c.HandleEvent(event(t, "state_changed", map[string]any{
						"entity_id": "light.kitchen",
						"new_state": map[string]any{"entity_id": "light.kitchen", "state": "on"},
					}))
				} else {
					_, _ = c.Lookup("light.kitchen")
					_ = c.All()
				}
			}
		})
	}
	wg.Wait()
}

func TestReturnedDevicesAreCopies(t *testing.T) {
	c := loaded(t, house())
	d, _ := c.Lookup("light.kitchen")
	d.Attributes["friendly_name"] = "changed"
	again, _ := c.Lookup("light.kitchen")
	if again.Attributes["friendly_name"] != "Kitchen" {
		t.Error("caller changed the catalog through a returned map")
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// blockingSource stops inside GetStates until released, to deliver events mid-refresh.
type blockingSource struct {
	*fakeSource
	entered, release chan struct{}
}

func (b blockingSource) GetStates(ctx context.Context) ([]ha.State, error) {
	states, err := b.fakeSource.GetStates(ctx)
	b.entered <- struct{}{}
	<-b.release
	return states, err
}

func TestEventsDuringARefreshAreNotLost(t *testing.T) {
	src := blockingSource{fakeSource: house(), entered: make(chan struct{}), release: make(chan struct{})}
	c := New(src, nil)
	done := make(chan error, 1)
	go func() { done <- c.Refresh(context.Background()) }()
	<-src.entered // the snapshot (kitchen light "off") is fetched; now the light turns on
	c.HandleEvent(event(t, "state_changed", map[string]any{
		"entity_id": "light.kitchen",
		"new_state": map[string]any{"entity_id": "light.kitchen", "state": "on"},
	}))
	close(src.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if d, _ := c.Lookup("light.kitchen"); d.State != "on" {
		t.Errorf("light.kitchen is %q after the refresh, want the newer state on", d.State)
	}
}

func TestInvalidateUntilTheNextRefresh(t *testing.T) {
	c := loaded(t, house())
	c.Invalidate()
	if c.Ready() {
		t.Error("ready after Invalidate")
	}
	if err := c.Refresh(context.Background()); err != nil || !c.Ready() {
		t.Errorf("not ready after a refresh: %v", err)
	}
}

func TestAreasAndNames(t *testing.T) {
	src := house()
	src.areas = []ha.Area{{AreaID: "living_room", Name: "Wohnzimmer"}, {AreaID: "kitchen", Name: "Küche"}, {AreaID: "", Name: "broken"}}
	c := New(src, nil)
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	areas := c.Areas()
	if len(areas) != 2 || areas[0] != (Area{ID: "kitchen", Name: "Küche"}) || areas[1].ID != "living_room" {
		t.Errorf("areas = %+v", areas)
	}
	areas[0].Name = "changed"
	if c.Areas()[0].Name != "Küche" {
		t.Error("Areas returned the catalog's own slice")
	}
	if d, _ := c.Lookup("light.kitchen"); d.Name() != "Kitchen" {
		t.Errorf("Name = %q", d.Name())
	}
	if d, _ := c.Lookup("switch.pool_pump"); d.Name() != "" {
		t.Errorf("Name without friendly_name = %q", d.Name())
	}
	src.set(func(f *fakeSource) { f.areaErr = errors.New("area registry down") })
	if err := c.Refresh(context.Background()); err == nil {
		t.Error("Refresh succeeded without the area registry")
	}
	if len(c.Areas()) != 2 {
		t.Error("a failed refresh dropped the areas")
	}
}

func TestRenamesAreReportedAfterTheRefresh(t *testing.T) {
	src := house()
	c := New(src, nil)
	reports := make(chan []Rename, 4)
	c.OnRefresh(func(renames []Rename) { reports <- renames })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, 5*time.Millisecond)
	c.RequestRefresh()
	if got := <-reports; len(got) != 0 {
		t.Fatalf("first refresh reported renames %v", got)
	}

	src.set(func(f *fakeSource) { f.states[0].EntityID = "light.kitchen_ceiling" })
	c.HandleEvent(event(t, "entity_registry_updated", map[string]any{"action": "update",
		"entity_id": "light.kitchen_ceiling", "old_entity_id": "light.kitchen", "changes": map[string]any{"entity_id": "light.kitchen"}}))
	got := <-reports
	if len(got) != 1 || got[0] != (Rename{Old: "light.kitchen", New: "light.kitchen_ceiling"}) {
		t.Fatalf("renames = %v", got)
	}
	if _, ok := c.Lookup("light.kitchen_ceiling"); !ok {
		t.Error("the hook ran before the new snapshot")
	}

	// A registry change without a rename reports an empty list: the directory changed.
	c.HandleEvent(event(t, "area_registry_updated", map[string]any{"action": "remove", "area_id": "garden"}))
	if got := <-reports; len(got) != 0 {
		t.Errorf("area change reported renames %v", got)
	}
}

func TestMalformedRenamesAreIgnored(t *testing.T) {
	for name, data := range map[string]map[string]any{
		"same id":          {"action": "update", "entity_id": "light.a", "old_entity_id": "light.a"},
		"no new id":        {"action": "update", "old_entity_id": "light.a"},
		"create":           {"action": "create", "entity_id": "light.b", "old_entity_id": "light.a"},
		"space":            {"action": "update", "entity_id": "light.b", "old_entity_id": "light. a"},
		"control":          {"action": "update", "entity_id": "light.b\n", "old_entity_id": "light.a"},
		"not a string":     {"action": "update", "entity_id": 7, "old_entity_id": "light.a"},
		"oversized new id": {"action": "update", "entity_id": "light." + strings.Repeat("a", 300), "old_entity_id": "light.a"},
	} {
		t.Run(name, func(t *testing.T) {
			c := New(house(), nil)
			c.HandleEvent(event(t, "entity_registry_updated", data))
			c.mu.Lock()
			defer c.mu.Unlock()
			if len(c.renames) != 0 {
				t.Errorf("kept %v", c.renames)
			}
		})
	}
}

func TestRenamesAreBounded(t *testing.T) {
	c := New(house(), nil)
	for i := range maxRenames + 10 {
		c.HandleEvent(event(t, "entity_registry_updated", map[string]any{"action": "update",
			"entity_id": fmt.Sprintf("light.new_%d", i), "old_entity_id": fmt.Sprintf("light.old_%d", i)}))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.renames) != maxRenames {
		t.Errorf("kept %d renames, want %d", len(c.renames), maxRenames)
	}
}

type holdingMarks map[string]bool

func (h holdingMarks) Critical(id string) bool { return h[id] }
func (h holdingMarks) Hold(oldID, newID string) bool {
	if !h[oldID] || h[newID] {
		return false
	}
	h[newID] = true
	return true
}

// The renamed entity is never decided on without its mark: the mark moves when the event
// arrives, before the refresh that brings the new ID.
func TestRenameMovesTheMarkBeforeTheRefresh(t *testing.T) {
	src := house()
	c := loaded(t, src)
	marks := holdingMarks{"light.kitchen": true}
	c.SetMarks(marks)
	c.HandleEvent(event(t, "entity_registry_updated", map[string]any{"action": "update",
		"entity_id": "light.kitchen_ceiling", "old_entity_id": "light.kitchen"}))
	// The state of the new ID may arrive before any refresh.
	c.HandleEvent(event(t, "state_changed", map[string]any{"entity_id": "light.kitchen_ceiling",
		"new_state": map[string]any{"entity_id": "light.kitchen_ceiling", "state": "on"}}))
	if d, ok := c.Lookup("light.kitchen_ceiling"); !ok || !d.Critical {
		t.Errorf("renamed entity = %+v, %v; want it critical at once", d, ok)
	}
}

type fakeAliases struct {
	mu      sync.Mutex
	held    []Rename
	observe []Rename
}

func (f *fakeAliases) Hold(rn Rename) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.held = append(f.held, rn)
	return true
}

func (f *fakeAliases) Observe([]ha.EntityEntry) []Rename {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := f.observe
	f.observe = nil
	return found
}

func (f *fakeAliases) Formers(id string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, rn := range f.held {
		if rn.New == id {
			out = append(out, rn.Old)
		}
	}
	return out
}

// A rename found by the event or by the registry IDs takes effect before the new ID can
// be decided on: the device carries its former ID.
func TestRenamesTakeEffectAtOnce(t *testing.T) {
	src := house()
	c := loaded(t, src)
	aliases := &fakeAliases{}
	c.SetAliases(aliases)
	c.HandleEvent(event(t, "entity_registry_updated", map[string]any{"action": "update",
		"entity_id": "light.kitchen_ceiling", "old_entity_id": "light.kitchen"}))
	c.HandleEvent(event(t, "state_changed", map[string]any{"entity_id": "light.kitchen_ceiling",
		"new_state": map[string]any{"entity_id": "light.kitchen_ceiling", "state": "on"}}))
	if d, ok := c.Lookup("light.kitchen_ceiling"); !ok || len(d.Formers) != 1 || d.Formers[0] != "light.kitchen" {
		t.Errorf("after the event: %+v, %v", d, ok)
	}

	// While Home-Mandate was away, the pool pump became the garden pump.
	aliases.mu.Lock()
	aliases.observe = []Rename{{Old: "switch.pool_pump", New: "switch.garden_pump"}}
	aliases.mu.Unlock()
	src.set(func(f *fakeSource) {
		for i := range f.states {
			if f.states[i].EntityID == "switch.pool_pump" {
				f.states[i].EntityID = "switch.garden_pump"
			}
		}
	})
	reports := make(chan []Rename, 1)
	c.OnRefresh(func(r []Rename) { reports <- r })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx, time.Millisecond)
	c.RequestRefresh()
	got := <-reports
	if !slices.Contains(got, Rename{Old: "switch.pool_pump", New: "switch.garden_pump"}) || !slices.Contains(got, Rename{Old: "light.kitchen", New: "light.kitchen_ceiling"}) {
		t.Errorf("reported = %v", got)
	}
	if d, ok := c.Lookup("switch.garden_pump"); !ok || len(d.Formers) != 1 {
		t.Errorf("after the refresh: %+v, %v", d, ok)
	}
}
