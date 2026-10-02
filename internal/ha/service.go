// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// allowedServices is the second line of defence behind the PEP's action table: the only
// services Home-Mandate calls for a device, each on exactly one entity of the same
// domain. Notifications (week 3) use their own, separate path.
var allowedServices = map[string]map[string]bool{
	"light":               {"turn_on": true, "turn_off": true},
	"switch":              {"turn_on": true, "turn_off": true},
	"climate":             {"set_temperature": true, "set_hvac_mode": true},
	"cover":               {"open_cover": true, "close_cover": true, "stop_cover": true, "set_cover_position": true},
	"lock":                {"lock": true, "unlock": true, "open": true},
	"alarm_control_panel": {"alarm_arm_home": true, "alarm_arm_away": true, "alarm_arm_night": true, "alarm_disarm": true},
	"media_player":        {"turn_on": true, "turn_off": true, "media_play": true, "media_pause": true, "volume_set": true},
	"scene":               {"turn_on": true},
	"script":              {"turn_on": true},
}

// targetKeys would widen a call beyond its one entity.
var targetKeys = []string{"entity_id", "device_id", "area_id", "floor_id", "label_id"}

var entityIDPattern = regexp.MustCompile(`^[a-z0-9_]+\.[a-z0-9_]+$`)

// ServiceCall is one service call on one entity.
type ServiceCall struct {
	Domain   string
	Service  string
	EntityID string
	Data     map[string]any
}

// CallService calls an allowlisted service on exactly one entity. It is never retried:
// if the connection is lost, the outcome is unknown and ErrDisconnected is returned.
func (c *Client) CallService(ctx context.Context, call ServiceCall) error {
	if err := checkServiceCall(call); err != nil {
		return err
	}
	data := maps.Clone(call.Data)
	if data == nil {
		data = map[string]any{}
	}
	_, err := c.request(ctx, command{Type: "call_service", Fields: map[string]any{
		"domain":          call.Domain,
		"service":         call.Service,
		"service_data":    data,
		"target":          map[string]any{"entity_id": call.EntityID},
		"return_response": false,
	}})
	return err
}

func checkServiceCall(call ServiceCall) error {
	if !allowedServices[call.Domain][call.Service] {
		return fmt.Errorf("%w: service %s.%s", ErrCommandNotAllowed, call.Domain, call.Service)
	}
	if !entityIDPattern.MatchString(call.EntityID) || !strings.HasPrefix(call.EntityID, call.Domain+".") {
		return fmt.Errorf("%w: target must be one %s entity", ErrCommandNotAllowed, call.Domain)
	}
	for _, k := range targetKeys {
		if _, ok := call.Data[k]; ok {
			return fmt.Errorf("%w: %s in service data", ErrCommandNotAllowed, k)
		}
	}
	return nil
}

// Connected reports whether the client is connected and authenticated right now.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.conn != nil
}
