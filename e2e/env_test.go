// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

// Package e2e runs the end-to-end scenarios of docs/TESTING.md section 3 against a real
// Home Assistant and the release image of Home-Mandate, both in containers.
//
//	make e2e                         # builds the image, uses podman or docker
//	E2E_IMAGE=… E2E_RUNTIME=docker   # use an existing image or another runtime
package e2e

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// haImage is pinned by digest (docs/TESTING.md section 3: official image, fixed version).
const haImage = "docker.io/homeassistant/home-assistant:2026.9.4@sha256:e47c978e1b801466e7f62f612fd552bc3a228e077b31a3f1c22c05cf63d754da"

const haConfiguration = `homeassistant:
  name: E2E
  time_zone: Europe/Berlin
  unit_system: metric
  country: DE
http:
  ssl_certificate: /config/certs/cert.pem
  ssl_key: /config/certs/key.pem
demo:
`

// env is the running environment shared by all scenarios.
var env struct {
	runtime  string
	id       string
	image    string
	certs    string
	roots    *x509.CertPool
	ca       *x509.Certificate // issues the certificates of the test, also a renewal
	caKey    *ecdsa.PrivateKey
	haURL    string // https://127.0.0.1:<port>
	haToken  string // long-lived token of the onboarding admin
	mcpURL   string // https://localhost:<port>/mcp
	public   string // https://localhost:<port>, HM_PUBLIC_URL
	users    map[string]*haUser
	ha, hm   string // container names
	ingress  string // the Ingress stand-in (tools/ingressproxy) at ingressIP
	uiURL    string // http://127.0.0.1:<port> of the stand-in
	uiPath   string // /api/hassio_ingress/<token>
	uiDirect string // http://127.0.0.1:<port> of the gateway's UI listener, bypassing Ingress
	network  string
	volume   string
	secrets  []string // must never appear in logs
	teardown []func()
}

func TestMain(m *testing.M) {
	code := 1
	if err := setUp(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e set-up failed:", err)
		dumpLogs()
	} else {
		code = m.Run()
		if code != 0 {
			dumpLogs()
		}
	}
	for i := len(env.teardown) - 1; i >= 0; i-- {
		env.teardown[i]()
	}
	os.Exit(code)
}

func setUp() error {
	env.runtime = os.Getenv("E2E_RUNTIME")
	if env.runtime == "" {
		env.runtime = "podman"
		if _, err := exec.LookPath("podman"); err != nil {
			env.runtime = "docker"
		}
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	env.id = hex.EncodeToString(b[:])
	env.ha, env.hm = "hm-e2e-ha-"+env.id, "hm-e2e-gw-"+env.id
	env.network, env.volume = "hm-e2e-net-"+env.id, "hm-e2e-data-"+env.id

	for _, step := range []func() error{prepareImage, makeCertificates, createNetwork, startHA, onboard, createUsers, startGateway, startIngress} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

func run(args ...string) (string, error) {
	return runInput("", args...)
}

func runInput(stdin string, args ...string) (string, error) {
	cmd := exec.Command(env.runtime, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %w: %s", env.runtime, strings.Join(args, " "), err, errOut.String())
	}
	return out.String(), nil
}

func prepareImage() error {
	env.image = os.Getenv("E2E_IMAGE")
	if env.image != "" {
		return nil
	}
	env.image = "localhost/home-mandate:e2e"
	_, err := run("build", "-q", "--build-arg", "VERSION=e2e", "-t", env.image, "..")
	return err
}

// makeCertificates creates a test CA and one server certificate for Home Assistant
// ("homeassistant") and the MCP endpoint ("localhost", 127.0.0.1).
func makeCertificates() error {
	dir, err := os.MkdirTemp("", "hm-e2e-certs-")
	if err != nil {
		return err
	}
	env.certs = dir
	env.teardown = append(env.teardown, func() { _ = os.RemoveAll(dir) })

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "home-mandate e2e CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "homeassistant"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		DNSNames: []string{"homeassistant", "localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)
	files := map[string][]byte{
		"ca.pem":   pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		"cert.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		"key.pem":  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}
	for name, data := range files {
		mode := os.FileMode(0o644)
		if name == "key.pem" {
			mode = 0o600 // the containers run as root (rootless: mapped to this user)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, mode); err != nil {
			return err
		}
	}
	env.roots = x509.NewCertPool()
	env.roots.AddCert(caCert)
	env.ca, env.caKey = caCert, caKey
	return nil
}

// supervisorSubnet is the hassio network of Home Assistant OS. The Ingress stand-in gets
// a fixed address in it that is deliberately not the Supervisor's: container mode trusts
// the proxy named in HM_INGRESS_PROXY (decision U2). The other containers get addresses
// from dynamicRange only.
const (
	supervisorSubnet = "172.30.32.0/23"
	dynamicRange     = "172.30.33.0/24"
	ingressIP        = "172.30.32.50"
)

func createNetwork() error {
	if _, err := run("network", "create", "--subnet", supervisorSubnet, "--ip-range", dynamicRange, env.network); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("network", "rm", "-f", env.network) })
	return nil
}

func hostPort(container, port string) (string, error) {
	out, err := run("port", container, port)
	if err != nil {
		return "", err
	}
	line := strings.TrimSpace(strings.Split(strings.TrimSpace(out), "\n")[0])
	_, p, err := net.SplitHostPort(line)
	if err != nil {
		return "", fmt.Errorf("port %s of %s: %q", port, container, out)
	}
	return net.JoinHostPort("127.0.0.1", p), nil
}

func startHA() error {
	dir, err := os.MkdirTemp("", "hm-e2e-ha-")
	if err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _ = os.RemoveAll(dir) })
	if err := os.WriteFile(filepath.Join(dir, "configuration.yaml"), []byte(haConfiguration), 0o644); err != nil {
		return err
	}
	if _, err := run("run", "-d", "--name", env.ha, "--network", env.network, "--network-alias", "homeassistant",
		"-p", "127.0.0.1::8123", "-v", dir+":/config", "-v", env.certs+":/config/certs:ro", haImage); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("rm", "-f", env.ha) })
	addr, err := hostPort(env.ha, "8123")
	if err != nil {
		return err
	}
	env.haURL = "https://" + addr
	return waitHTTP(env.haURL+"/api/onboarding", 3*time.Minute)
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: env.roots, MinVersion: tls.VersionTLS12}}}
}

func waitHTTP(target string, limit time.Duration) error {
	deadline := time.Now().Add(limit)
	for {
		resp, err := httpClient().Get(target)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s not ready: %v", target, err)
		}
		time.Sleep(time.Second)
	}
}

// onboard creates the owner account and a long-lived token for Home-Mandate.
func onboard() error {
	clientID := env.haURL + "/"
	body, _ := json.Marshal(map[string]string{"client_id": clientID, "name": "E2E Admin", "username": "e2e-admin",
		"password": "e2e-" + env.id + "-password", "language": "en"})
	resp, err := httpClient().Post(env.haURL+"/api/onboarding/users", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	var created struct {
		AuthCode string `json:"auth_code"`
	}
	err = json.NewDecoder(resp.Body).Decode(&created)
	resp.Body.Close()
	if err != nil || created.AuthCode == "" {
		return fmt.Errorf("onboarding: status %d, %v", resp.StatusCode, err)
	}
	resp, err = httpClient().PostForm(env.haURL+"/auth/token", url.Values{
		"grant_type": {"authorization_code"}, "code": {created.AuthCode}, "client_id": {clientID}})
	if err != nil {
		return err
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
	}
	err = json.NewDecoder(resp.Body).Decode(&tokens)
	resp.Body.Close()
	if err != nil || tokens.AccessToken == "" {
		return fmt.Errorf("token: status %d, %v", resp.StatusCode, err)
	}
	llat, err := longLivedToken(tokens.AccessToken)
	if err != nil {
		return err
	}
	env.haToken = llat
	env.secrets = append(env.secrets, llat, tokens.AccessToken)
	return os.WriteFile(filepath.Join(env.certs, "ha-token"), []byte(llat), 0o600)
}

func longLivedToken(accessToken string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	wsURL := "wss" + strings.TrimPrefix(env.haURL, "https") + "/api/websocket"
	conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPClient: httpClient()})
	if err != nil {
		return "", err
	}
	defer conn.CloseNow()
	conn.SetReadLimit(16 << 20)
	read := func() (map[string]any, error) {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return nil, err
		}
		var m map[string]any
		return m, json.Unmarshal(data, &m)
	}
	write := func(v any) error {
		data, _ := json.Marshal(v)
		return conn.Write(ctx, websocket.MessageText, data)
	}
	if _, err := read(); err != nil { // auth_required
		return "", err
	}
	if err := write(map[string]any{"type": "auth", "access_token": accessToken}); err != nil {
		return "", err
	}
	if m, err := read(); err != nil || m["type"] != "auth_ok" {
		return "", fmt.Errorf("auth: %v %v", m["type"], err)
	}
	if err := write(map[string]any{"id": 1, "type": "auth/long_lived_access_token", "client_name": "home-mandate e2e", "lifespan": 1}); err != nil {
		return "", err
	}
	m, err := read()
	if err != nil || m["success"] != true {
		return "", fmt.Errorf("long-lived token: %v", err)
	}
	token, _ := m["result"].(string)
	return token, nil
}

func startGateway() error {
	if _, err := run("volume", "create", env.volume); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("volume", "rm", "-f", env.volume) })
	port, err := freePort()
	if err != nil {
		return err
	}
	env.public = "https://localhost:" + port
	if _, err := run("run", "-d", "--name", env.hm, "--network", env.network, "-p", "127.0.0.1:"+port+":8765", "-p", "127.0.0.1::8099",
		"-e", "HM_INGRESS_ADDR=:8099", "-e", "HM_INGRESS_PROXY="+ingressIP,
		"-v", env.volume+":/data", "-v", env.certs+":/certs:ro",
		"-e", "HM_HA_URL=wss://homeassistant:8123/api/websocket",
		"-e", "HM_HA_TOKEN_FILE=/certs/ha-token",
		"-e", "HM_HA_CA_FILE=/certs/ca.pem",
		"-e", "HM_TLS_CERT=/certs/cert.pem", "-e", "HM_TLS_KEY=/certs/key.pem",
		"-e", "HM_LOG_LEVEL=debug",
		"-e", "HM_PUBLIC_URL="+env.public,
		"-e", "HM_APPROVAL_TIMEOUT=30",
		env.image); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("rm", "-f", env.hm) })
	env.mcpURL = env.public + "/mcp"
	if err := waitHTTP(env.mcpURL, time.Minute); err != nil { // 401 means it is up
		return err
	}
	direct, err := hostPort(env.hm, "8099")
	if err != nil {
		return err
	}
	env.uiDirect = "http://" + direct
	return nil
}

// startIngress builds the Ingress stand-in from tools/ingressproxy and runs it at
// ingressIP, in front of the gateway's UI listener.
func startIngress() error {
	dir, err := os.MkdirTemp("", "hm-e2e-ingress-")
	if err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _ = os.RemoveAll(dir) })
	build := exec.Command("go", "build", "-trimpath", "-o", filepath.Join(dir, "ingressproxy"), "../tools/ingressproxy")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		return fmt.Errorf("build ingressproxy: %w: %s", err, out)
	}
	containerfile := "FROM scratch\nCOPY ingressproxy /ingressproxy\nENTRYPOINT [\"/ingressproxy\"]\n"
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte(containerfile), 0o644); err != nil {
		return err
	}
	image := "localhost/hm-e2e-ingress:" + env.id
	if _, err := run("build", "-q", "-f", filepath.Join(dir, "Containerfile"), "-t", image, dir); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("rmi", "-f", image) })
	env.ingress = "hm-e2e-ingress-" + env.id
	token := randomHex(32)
	env.uiPath = "/api/hassio_ingress/" + token
	if _, err := run("run", "-d", "--name", env.ingress, "--network", env.network, "--ip", ingressIP, "-p", "127.0.0.1::8080",
		"-v", env.certs+":/certs:ro", image, "-listen", ":8080", "-target", "http://"+env.hm+":8099",
		"-ha", "https://homeassistant:8123", "-ca", "/certs/ca.pem", "-token", token); err != nil {
		return err
	}
	env.teardown = append(env.teardown, func() { _, _ = run("rm", "-f", env.ingress) })
	addr, err := hostPort(env.ingress, "8080")
	if err != nil {
		return err
	}
	env.uiURL = "http://" + addr
	return waitHTTP(env.uiURL+"/", time.Minute) // 404 means it is up
}

// freePort returns a free TCP port on the host for the gateway: its public URL must be
// known before it starts.
func freePort() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	return port, err
}

// cli runs a Home-Mandate administration command inside the gateway container.
func cli(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	out, err := runInput(stdin, append([]string{"exec", "-i", env.hm, "/home-mandate"}, args...)...)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// haState reads an entity state through the REST API of Home Assistant.
func haState(t *testing.T, entityID string) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, env.haURL+"/api/states/"+entityID, nil)
	req.Header.Set("Authorization", "Bearer "+env.haToken)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var s struct {
		State string `json:"state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
		t.Fatalf("state of %s: %v", entityID, err)
	}
	return s.State
}

func eventually(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func dumpLogs() {
	for _, c := range []string{env.hm, env.ha, env.ingress} {
		if c == "" {
			continue
		}
		lines := strings.Split(strings.TrimSpace(logsOf(c)), "\n") // stdout and stderr
		fmt.Fprintf(os.Stderr, "--- logs of %s ---\n%s\n", c, strings.Join(lines[max(0, len(lines)-80):], "\n"))
	}
}

func logsOf(container string) string {
	cmd := exec.Command(env.runtime, "logs", container)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	_ = cmd.Run()
	return buf.String()
}
