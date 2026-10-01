// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// State is an entity state from get_states.
type State struct {
	EntityID    string         `json:"entity_id"`
	State       string         `json:"state"`
	Attributes  map[string]any `json:"attributes"`
	LastChanged time.Time      `json:"last_changed"`
	LastUpdated time.Time      `json:"last_updated"`
	Context     EventContext   `json:"context"`
}

// EntityEntry is an entry of the entity registry.
type EntityEntry struct {
	ID             string   `json:"id"`
	EntityID       string   `json:"entity_id"`
	DeviceID       string   `json:"device_id"`
	AreaID         string   `json:"area_id"`
	Platform       string   `json:"platform"`
	Name           string   `json:"name"`
	EntityCategory string   `json:"entity_category"`
	DisabledBy     string   `json:"disabled_by"`
	HiddenBy       string   `json:"hidden_by"`
	HasEntityName  bool     `json:"has_entity_name"`
	Labels         []string `json:"labels"`
}

// Device is an entry of the device registry.
type Device struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	NameByUser   string   `json:"name_by_user"`
	AreaID       string   `json:"area_id"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	DisabledBy   string   `json:"disabled_by"`
	Labels       []string `json:"labels"`
}

// Area is an entry of the area registry.
type Area struct {
	AreaID  string   `json:"area_id"`
	Name    string   `json:"name"`
	FloorID string   `json:"floor_id"`
	Aliases []string `json:"aliases"`
	Labels  []string `json:"labels"`
}

// Floor is an entry of the floor registry.
type Floor struct {
	FloorID string   `json:"floor_id"`
	Name    string   `json:"name"`
	Level   *int     `json:"level"`
	Aliases []string `json:"aliases"`
}

// Label is an entry of the label registry.
type Label struct {
	LabelID string `json:"label_id"`
	Name    string `json:"name"`
}

// HAConfig is the part of get_config Home-Mandate uses: household time zone, language
// and units.
type HAConfig struct {
	Version      string            `json:"version"`
	LocationName string            `json:"location_name"`
	TimeZone     string            `json:"time_zone"`
	Language     string            `json:"language"`
	Country      string            `json:"country"`
	UnitSystem   map[string]string `json:"unit_system"`
}

// Event is an event delivered to a subscription.
type Event struct {
	EventType string          `json:"event_type"`
	Data      json.RawMessage `json:"data"`
	Origin    string          `json:"origin"`
	TimeFired time.Time       `json:"time_fired"`
	Context   EventContext    `json:"context"`
}

// EventContext identifies who caused a state change or event. UserID is how approval
// answers are attributed to a human (ARCHITECTURE section 7).
type EventContext struct {
	ID       string `json:"id"`
	ParentID string `json:"parent_id"`
	UserID   string `json:"user_id"`
}

// GetStates returns all entity states.
func (c *Client) GetStates(ctx context.Context) ([]State, error) {
	return call[[]State](ctx, c, "get_states")
}

// ListEntities returns the entity registry.
func (c *Client) ListEntities(ctx context.Context) ([]EntityEntry, error) {
	return call[[]EntityEntry](ctx, c, "config/entity_registry/list")
}

// ListDevices returns the device registry.
func (c *Client) ListDevices(ctx context.Context) ([]Device, error) {
	return call[[]Device](ctx, c, "config/device_registry/list")
}

// ListAreas returns the area registry.
func (c *Client) ListAreas(ctx context.Context) ([]Area, error) {
	return call[[]Area](ctx, c, "config/area_registry/list")
}

// ListFloors returns the floor registry.
func (c *Client) ListFloors(ctx context.Context) ([]Floor, error) {
	return call[[]Floor](ctx, c, "config/floor_registry/list")
}

// ListLabels returns the label registry.
func (c *Client) ListLabels(ctx context.Context) ([]Label, error) {
	return call[[]Label](ctx, c, "config/label_registry/list")
}

// GetConfig returns the Home Assistant configuration.
func (c *Client) GetConfig(ctx context.Context) (HAConfig, error) {
	return call[HAConfig](ctx, c, "get_config")
}

func call[T any](ctx context.Context, c *Client, typ string) (T, error) {
	var out T
	raw, err := c.request(ctx, command{Type: typ})
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("%w: %s result: %w", ErrProtocol, typ, err)
	}
	return out, nil
}
