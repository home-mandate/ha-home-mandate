// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

var (
	// ErrCommandNotAllowed means a command or event type is not on the allowlist and was
	// not sent.
	ErrCommandNotAllowed = errors.New("home assistant: command not allowed")
	// ErrProtocol means Home Assistant sent a message that violates the protocol.
	ErrProtocol = errors.New("home assistant: protocol error")
)

// Message types sent by Home Assistant.
const (
	typeAuthRequired = "auth_required"
	typeAuthOK       = "auth_ok"
	typeAuthInvalid  = "auth_invalid"
	typeResult       = "result"
	typeEvent        = "event"
	typePong         = "pong"
)

// Event types Home-Mandate subscribes to.
const (
	EventStateChanged                = "state_changed"
	EventMobileAppNotificationAction = "mobile_app_notification_action"
)

// allowedCommands is the fixed allowlist of WebSocket commands from docs/ARCHITECTURE.md
// section 11.2. Home-Mandate's HA user is an admin; this list is what limits it.
var allowedCommands = map[string]bool{
	"get_states":                  true,
	"subscribe_entities":          true,
	"config/entity_registry/list": true,
	"config/device_registry/list": true,
	"config/area_registry/list":   true,
	"config/floor_registry/list":  true,
	"config/label_registry/list":  true,
	"call_service":                true,
	"auth/current_user":           true,
	"get_config":                  true,
	"subscribe_events":            true,
	"unsubscribe_events":          true,
	"ping":                        true,
}

// allowedEvents limits subscribe_events (ARCHITECTURE section 11.2).
var allowedEvents = map[string]bool{
	EventStateChanged:                true,
	"entity_registry_updated":        true,
	"device_registry_updated":        true,
	"area_registry_updated":          true,
	"floor_registry_updated":         true,
	"label_registry_updated":         true,
	EventMobileAppNotificationAction: true,
}

// command is a request to Home Assistant; the client adds the id.
type command struct {
	Type   string
	Fields map[string]any
}

// checkAllowed rejects every command that is not on the allowlist, before it is sent.
func checkAllowed(cmd command) error {
	if !allowedCommands[cmd.Type] {
		return fmt.Errorf("%w: %q", ErrCommandNotAllowed, cmd.Type)
	}
	if _, ok := cmd.Fields["id"]; ok {
		return fmt.Errorf("%w: field id", ErrCommandNotAllowed)
	}
	if _, ok := cmd.Fields["type"]; ok {
		return fmt.Errorf("%w: field type", ErrCommandNotAllowed)
	}
	if cmd.Type == "subscribe_events" {
		eventType, _ := cmd.Fields["event_type"].(string)
		if !allowedEvents[eventType] {
			return fmt.Errorf("%w: event type %q", ErrCommandNotAllowed, eventType)
		}
	}
	return nil
}

func encodeCommand(id int64, cmd command) ([]byte, error) {
	msg := make(map[string]any, len(cmd.Fields)+2)
	for k, v := range cmd.Fields {
		msg[k] = v
	}
	msg["id"] = id
	msg["type"] = cmd.Type
	return json.Marshal(msg)
}

// message is any message from Home Assistant.
type message struct {
	ID      int64           `json:"id"`
	Type    string          `json:"type"`
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Error   *CommandError   `json:"error"`
	Event   json.RawMessage `json:"event"`
}

// decodeMessage parses one message. Unknown types are returned for the caller to ignore;
// anything that is not a JSON object with a type, and responses without a positive id,
// are protocol errors.
func decodeMessage(data []byte) (message, error) {
	var m message
	if !bytes.HasPrefix(bytes.TrimLeft(data, " \t\r\n"), []byte("{")) {
		return m, fmt.Errorf("%w: not a JSON object", ErrProtocol)
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return message{}, fmt.Errorf("%w: %w", ErrProtocol, err)
	}
	if m.Type == "" {
		return message{}, fmt.Errorf("%w: message without type", ErrProtocol)
	}
	switch m.Type {
	case typeResult, typeEvent, typePong:
		if m.ID <= 0 {
			return message{}, fmt.Errorf("%w: %s without id", ErrProtocol, m.Type)
		}
	}
	return m, nil
}

// CommandError is an error result from Home Assistant.
type CommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error reports only the code; Message may name entities and stays out of error chains
// that could reach agents.
func (e *CommandError) Error() string {
	return "home assistant: command failed: " + e.Code
}
