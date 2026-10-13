// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// coreInfoLimit bounds the Supervisor's answer.
const coreInfoLimit = 64 << 10

// slugPattern is an app's slug: a repository prefix and the name (local_…, a0d7b954_…).
var slugPattern = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)

// CoreInfo asks the Supervisor at supervisor (http://supervisor in app mode) how Home
// Assistant serves its HTTP API: the port and whether it speaks TLS itself. Home
// Assistant's port and certificate are settings of the household, so the address on the
// Supervisor's network cannot be assumed.
func CoreInfo(ctx context.Context, supervisor string, token Secret, plaintext Plaintext) (port int, tls bool, err error) {
	var data struct {
		Port int  `json:"port"`
		SSL  bool `json:"ssl"`
	}
	if err := supervisorGet(ctx, supervisor, "/core/info", token, plaintext, &data); err != nil {
		return 0, false, fmt.Errorf("home assistant: core info: %w", err)
	}
	if data.Port < 1 || data.Port > 65535 {
		return 0, false, fmt.Errorf("home assistant: core info: no port")
	}
	return data.Port, data.SSL, nil
}

// AppSlug asks the Supervisor for the slug of the app Home-Mandate runs as; its panel in
// Home Assistant's frontend is named after it (issue #49). The default role of the
// Supervisor API allows it.
func AppSlug(ctx context.Context, supervisor string, token Secret, plaintext Plaintext) (string, error) {
	var data struct {
		Slug string `json:"slug"`
	}
	if err := supervisorGet(ctx, supervisor, "/addons/self/info", token, plaintext, &data); err != nil {
		return "", fmt.Errorf("home assistant: app info: %w", err)
	}
	if !slugPattern.MatchString(data.Slug) {
		return "", fmt.Errorf("home assistant: app info: no slug")
	}
	return data.Slug, nil
}

// supervisorGet reads the data of a GET request to the Supervisor's API into out.
func supervisorGet(ctx context.Context, supervisor, path string, token Secret, plaintext Plaintext, out any) error {
	client, err := HTTPClient(supervisor, nil, plaintext, "")
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, supervisor+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	var answer struct {
		Result string          `json:"result"`
		Data   json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, coreInfoLimit)).Decode(&answer); err != nil {
		return err
	}
	if answer.Result != "ok" {
		return fmt.Errorf("result %q", answer.Result)
	}
	return json.Unmarshal(answer.Data, out)
}
