// SPDX-License-Identifier: AGPL-3.0-or-later

// Command ctserver stands in for Cert Spotter and crt.sh in the end-to-end tests (it is no
// part of Home-Mandate). It serves the certificates in a directory, *.pem in the order of
// their file names, as both would: GET /v1/issuances with domain, match_wildcards and the
// after cursor (Cert Spotter), and GET /?q=…&output=json and /?d=<id> (crt.sh). The
// directory is read on every request, so that a test adds a certificate by writing a file.
// Like the client metadata server, it runs on port 443 in a test network the gateway's
// rules do not reserve, with a certificate of the test CA for both host names.
//
//	ctserver -listen :443 -cert /certs/ct-src-cert.pem -key /certs/ct-src-key.pem -dir /issued
package main

import (
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
)

func main() {
	if err := run(os.Args[1:], os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "ctserver:", err)
		os.Exit(1)
	}
}

func run(args []string, stderr io.Writer) error {
	flags := flag.NewFlagSet("ctserver", flag.ContinueOnError)
	flags.SetOutput(stderr)
	listen := flags.String("listen", ":443", "listen address")
	cert := flags.String("cert", "", "PEM certificate for api.certspotter.com and crt.sh")
	key := flags.String("key", "", "PEM private key of the certificate")
	dir := flags.String("dir", "", "directory of the certificates to serve (*.pem)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *cert == "" || *key == "" || *dir == "" {
		return errors.New("need -cert, -key and -dir")
	}
	pair, err := tls.LoadX509KeyPair(*cert, *key)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: *listen, Handler: newHandler(*dir), ReadHeaderTimeout: 10 * time.Second,
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}
	return srv.ListenAndServeTLS("", "")
}

// issued is one certificate of the directory; its ID is its position, from 1.
type issued struct {
	id  int
	der []byte
	ctwatch.Parsed
}

// load reads the certificates of dir in the order of their file names.
func load(dir string) ([]issued, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.pem"))
	if err != nil {
		return nil, err
	}
	slices.Sort(files)
	out := make([]issued, 0, len(files))
	for i, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		block, _ := pem.Decode(data)
		if block == nil {
			return nil, fmt.Errorf("%s: no PEM", f)
		}
		p, err := ctwatch.ParseCert(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f, err)
		}
		out = append(out, issued{id: i + 1, der: block.Bytes, Parsed: p})
	}
	return out, nil
}

// matches tells whether a certificate is found for a query of name: by the name itself
// or, with wildcards, by a wildcard of its parent.
func matches(c issued, name string, wildcards bool) bool {
	for _, n := range c.Names {
		if n == name || (wildcards && ctwatch.Covers(n, name)) {
			return true
		}
	}
	return false
}

func newHandler(dir string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/issuances", func(w http.ResponseWriter, r *http.Request) {
		all, err := load(dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		after, _ := strconv.Atoi(q.Get("after"))
		out := []map[string]any{}
		for _, c := range all {
			if c.id <= after || !matches(c, strings.ToLower(q.Get("domain")), q.Get("match_wildcards") == "true") {
				continue
			}
			out = append(out, map[string]any{"id": strconv.Itoa(c.id), "tbs_sha256": hex.EncodeToString(c.Digest[:]),
				"pubkey_sha256": hex.EncodeToString(c.PublicKey[:]), "dns_names": c.Names,
				"not_before": c.NotBefore.Format(time.RFC3339), "not_after": c.NotAfter.Format(time.RFC3339),
				"cert_der": base64.StdEncoding.EncodeToString(c.der), "issuer": map[string]string{"name": c.Issuer}})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		all, err := load(dir)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		q := r.URL.Query()
		if d := q.Get("d"); d != "" {
			id, _ := strconv.Atoi(d)
			for _, c := range all {
				if c.id == id {
					_ = pem.Encode(w, &pem.Block{Type: "CERTIFICATE", Bytes: c.der})
					return
				}
			}
			http.NotFound(w, r)
			return
		}
		out := []map[string]int{}
		for _, c := range all {
			if matches(c, strings.ToLower(q.Get("q")), false) {
				out = append(out, map[string]int{"id": c.id})
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	return mux
}
