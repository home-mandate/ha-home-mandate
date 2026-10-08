// SPDX-License-Identifier: AGPL-3.0-or-later

// Command cimdserver serves one Client ID Metadata Document for the end-to-end tests (it
// is no part of Home-Mandate): an agent's client ID is the https URL of its metadata, and
// the gateway fetches it before it admits the agent. The release image fetches only from
// public addresses on port 443, so the tests run this server on port 443 in a test
// network whose subnet the gateway's rules do not reserve, with a certificate of the test
// CA that the gateway trusts in the test run only.
//
//	cimdserver -listen :443 -cert /certs/cimd-cert.pem -key /certs/cimd-key.pem \
//	  -client-id https://cimd.example.test/agent.json -name "Agent" \
//	  -redirect-uri http://127.0.0.1/callback
package main

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "cimdserver:", err)
		os.Exit(1)
	}
}

// redirectList collects repeated -redirect-uri flags.
type redirectList []string

func (r *redirectList) String() string { return strings.Join(*r, ",") }

func (r *redirectList) Set(v string) error {
	*r = append(*r, v)
	return nil
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("cimdserver", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", ":443", "listen address")
	cert := flags.String("cert", "", "PEM certificate for the host of the client ID")
	key := flags.String("key", "", "PEM private key of the certificate")
	clientID := flags.String("client-id", "", "the client ID: the https URL of the document")
	name := flags.String("name", "Home-Mandate E2E agent", "client_name in the document")
	var redirects redirectList
	flags.Var(&redirects, "redirect-uri", "a registered redirect URI, repeatable")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cert == "" || *key == "" {
		return errors.New("need -cert and -key")
	}
	h, err := newHandler(*clientID, *name, redirects)
	if err != nil {
		return err
	}
	pair, err := tls.LoadX509KeyPair(*cert, *key)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: h, ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}
	return srv.ListenAndServeTLS("", "")
}

// newHandler answers GET and HEAD of the document's path and nothing else.
func newHandler(clientID, name string, redirects []string) (http.Handler, error) {
	path, body, err := newDocument(clientID, name, redirects)
	if err != nil {
		return nil, err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(body)
	})
	return mux, nil
}

// newDocument returns the path of clientID and its metadata document, after the same
// basic rules the gateway applies: https, a path, no query or fragment.
func newDocument(clientID, name string, redirects []string) (string, []byte, error) {
	u, err := url.Parse(clientID)
	if err != nil {
		return "", nil, fmt.Errorf("client ID: %w", err)
	}
	if u.Scheme != "https" || u.Host == "" || u.Path == "" || u.Path == "/" || u.RawQuery != "" || u.Fragment != "" {
		return "", nil, errors.New("client ID must be an https URL with a path and without query or fragment")
	}
	if len(redirects) == 0 {
		return "", nil, errors.New("need at least one -redirect-uri")
	}
	for _, r := range redirects {
		if r == "" {
			return "", nil, errors.New("empty redirect URI")
		}
	}
	body, err := json.Marshal(map[string]any{
		"client_id":                  clientID,
		"client_name":                name,
		"redirect_uris":              redirects,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	return u.Path, body, err
}
