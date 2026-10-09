// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"io"
	"net/http"
	"strconv"
	"time"
)

// The gateway limits the requests a sender may make to its endpoints without a token
// (sign-ins, pairings, codes). The suite admits many agents from one address within a
// minute, so its OAuth and browser clients behave as a well-behaved client does: after a
// 429 they wait as long as Retry-After says and send the request again. A refused request
// was not processed, so sending it again is safe.

const (
	politeRetries = 3
	politeMaxWait = 70 * time.Second
	// politeTimeout bounds a request including its waits.
	politeTimeout = 3 * time.Minute
)

type polite struct{ next http.RoundTripper }

func (p polite) RoundTrip(r *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := p.next.RoundTrip(r)
		if err != nil || resp.StatusCode != http.StatusTooManyRequests || attempt == politeRetries {
			return resp, err
		}
		seconds, perr := strconv.Atoi(resp.Header.Get("Retry-After"))
		wait := time.Duration(seconds) * time.Second
		if perr != nil || wait <= 0 || wait > politeMaxWait || (r.Body != nil && r.GetBody == nil) {
			return resp, nil
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		select {
		case <-r.Context().Done():
			return nil, r.Context().Err()
		case <-time.After(wait):
		}
		if r.GetBody != nil {
			body, err := r.GetBody()
			if err != nil {
				return nil, err
			}
			r = r.Clone(r.Context())
			r.Body = body
		}
	}
}
