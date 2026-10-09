// SPDX-License-Identifier: AGPL-3.0-or-later

package ha

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// coreInfoLimit bounds the Supervisor's answer.
const coreInfoLimit = 64 << 10

// CoreInfo asks the Supervisor at supervisor (http://supervisor in app mode) how Home
// Assistant serves its HTTP API: the port and whether it speaks TLS itself. Home
// Assistant's port and certificate are settings of the household, so the address on the
// Supervisor's network cannot be assumed.
func CoreInfo(ctx context.Context, supervisor string, token Secret, plaintext Plaintext) (port int, tls bool, err error) {
	client, err := HTTPClient(supervisor, nil, plaintext, "")
	if err != nil {
		return 0, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, supervisor+"/core/info", nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Authorization", "Bearer "+string(token))
	resp, err := client.Do(req)
	if err != nil {
		return 0, false, fmt.Errorf("home assistant: core info: %w", err)
	}
	defer resp.Body.Close()
	var answer struct {
		Result string `json:"result"`
		Data   struct {
			Port int  `json:"port"`
			SSL  bool `json:"ssl"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("home assistant: core info: status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, coreInfoLimit)).Decode(&answer); err != nil {
		return 0, false, fmt.Errorf("home assistant: core info: %w", err)
	}
	if answer.Result != "ok" || answer.Data.Port < 1 || answer.Data.Port > 65535 {
		return 0, false, fmt.Errorf("home assistant: core info: no port")
	}
	return answer.Data.Port, answer.Data.SSL, nil
}
