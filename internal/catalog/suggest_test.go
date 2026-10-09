// SPDX-License-Identifier: AGPL-3.0-or-later

package catalog_test

import (
	"testing"

	"github.com/home-mandate/ha-home-mandate/internal/catalog"
)

func TestSuggestCritical(t *testing.T) {
	dev := func(id, name, class string) catalog.Device {
		attrs := map[string]any{}
		if name != "" {
			attrs["friendly_name"] = name
		}
		if class != "" {
			attrs["device_class"] = class
		}
		return catalog.Device{EntityID: id, Category: catalog.Category(id, class), Attributes: attrs}
	}
	for name, tt := range map[string]struct {
		device catalog.Device
		want   bool
	}{
		"switch for the garage":           {dev("switch.garage_opener", "Garage öffnen", ""), true},
		"switch named after a door":       {dev("switch.relay_1", "Haustür Summer", ""), true},
		"switch named in English":         {dev("switch.relay_2", "Front Door Buzzer", ""), true},
		"switch for a gate":               {dev("switch.relay_3", "Hoftor", ""), true},
		"gate as a word":                  {dev("switch.relay_4", "Tor Einfahrt", ""), true},
		"gate in a compound":              {dev("switch.relay_6", "Gartentor", ""), true},
		"motor is no gate":                {dev("switch.pump", "Pumpenmotor", ""), false},
		"investor is no gate":             {dev("switch.x", "Investor", ""), false},
		"outdoor is no door":              {dev("switch.outdoor_socket", "Outdoor socket", ""), false},
		"navigate is no gate":             {dev("script.navigate", "Navigate", ""), false},
		"gate of a property":              {dev("switch.y", "Einfahrtstor", ""), true},
		"umlaut spelled out":              {dev("switch.relay_5", "Kellertuer", ""), true},
		"entity ID only":                  {dev("switch.garage", "", ""), true},
		"door cover is critical anyway":   {dev("cover.patio", "Terrasse", "door"), false},
		"cover of a window":               {dev("cover.bath", "Bad", "window"), true},
		"script named after a door":       {dev("script.open_door", "Tür öffnen", ""), true},
		"plain switch":                    {dev("switch.coffee", "Kaffeemaschine", ""), false},
		"word that only contains tor":     {dev("switch.monitor", "Monitor", ""), false},
		"blind with its class":            {dev("cover.living", "Rollladen", "shutter"), false},
		"cover without a class is a gate": {dev("cover.living", "Rollladen", ""), false},
		"awning named after a door":       {dev("cover.door_awning", "Markise", "awning"), true},
		"lock is critical anyway":         {dev("lock.front_door", "Haustür", ""), false},
		"garage gate is critical anyway":  {dev("cover.garage", "Garagentor", "garage"), false},
		"sensor of a door is read only":   {dev("binary_sensor.front_door", "Haustür", "door"), false},
		"light by the door":               {dev("light.door", "Licht Haustür", ""), false},
	} {
		if got := catalog.SuggestCritical(tt.device); got != tt.want {
			t.Errorf("%s: SuggestCritical = %v, want %v", name, got, tt.want)
		}
	}
}
