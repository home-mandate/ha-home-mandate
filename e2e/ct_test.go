// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build e2e

package e2e

import (
	"context"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/home-mandate/ha-home-mandate/internal/ctwatch"
)

// Certificate Transparency monitoring (issue #66, CT part of #67) against the release
// image: a second gateway whose public name is a public one, and tools/ctserver standing
// in for Cert Spotter and crt.sh. The release image is unchanged: the sources' names point
// to the stand-in (--add-host) in the client metadata server's network, and its
// certificate comes from the test CA that SSL_CERT_FILE adds in the test run only.
const (
	ctName      = "hm.e2e.example.net"
	ctSourceIP  = "192.88.99.11"
	ctIngressIP = "172.30.32.51"
)

// ctGateway is the second gateway and its Ingress stand-in.
type ctGateway struct {
	name, issued   string
	uiURL, uiPath  string
	ownDigest      ctwatch.Digest
	foreignDigest  ctwatch.Digest
	foreignPEMFile string
}

func certDigest(t *testing.T, file string) ctwatch.Digest {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	p, err := ctwatch.ParseCert(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return p.Digest
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// startCTGateway issues the certificates, starts the stand-in of the sources with the
// gateway's own certificate and a foreign one in its logs, then the gateway with an
// approver added before its first round, and an Ingress stand-in in front of its UI.
func startCTGateway(t *testing.T) *ctGateway {
	t.Helper()
	for _, c := range []struct {
		serial int64
		prefix string
		names  []string
	}{{20, "ct-gw-", []string{ctName}}, {21, "ct-src-", []string{"api.certspotter.com", "crt.sh"}}, {22, "ct-foreign-", []string{ctName}}} {
		if err := issueCertificate(c.serial, c.prefix, c.names, nil); err != nil {
			t.Fatal(err)
		}
	}
	g := &ctGateway{name: "hm-e2e-ct-" + env.id}
	issued, err := os.MkdirTemp("", "hm-e2e-ct-issued-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(issued) })
	_ = os.Chmod(issued, 0o755)
	g.issued = issued
	copyFile(t, filepath.Join(env.certs, "ct-gw-cert.pem"), filepath.Join(issued, "1-own.pem"))
	g.ownDigest = certDigest(t, filepath.Join(env.certs, "ct-gw-cert.pem"))
	g.foreignPEMFile = filepath.Join(env.certs, "ct-foreign-cert.pem")
	g.foreignDigest = certDigest(t, g.foreignPEMFile)
	copyFile(t, g.foreignPEMFile, filepath.Join(issued, "2-foreign.pem"))

	image, err := buildTool("ctserver")
	if err != nil {
		t.Fatal(err)
	}
	source := "hm-e2e-ctsrc-" + env.id
	if _, err := run("run", "-d", "--name", source, "--network", env.cimdNet, "--ip", ctSourceIP, "-v", env.certs+":/certs:ro",
		"-v", issued+":/issued:ro", image, "-listen", ":443", "-cert", "/certs/ct-src-cert.pem", "-key", "/certs/ct-src-key.pem", "-dir", "/issued"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = run("rm", "-f", source) })

	data, err := os.MkdirTemp("", "hm-e2e-ct-data-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(data) })
	port, err := freePort()
	if err != nil {
		t.Fatal(err)
	}
	args := append([]string{"run", "-d", "--name", g.name}, gatewayUser()...)
	if _, err := run(append(args, "--network", env.network, "--network", env.cimdNet,
		"--add-host", "api.certspotter.com:"+ctSourceIP, "--add-host", "crt.sh:"+ctSourceIP,
		"-p", "127.0.0.1:"+port+":8765", "-e", "HM_INGRESS_ADDR=:8099", "-e", "HM_INGRESS_PROXY="+ctIngressIP,
		"-v", data+":/data", "-v", env.certs+":/certs:ro",
		"-e", "HM_HA_URL=wss://homeassistant:8123/api/websocket", "-e", "HM_HA_TOKEN_FILE=/certs/ha-token",
		"-e", "HM_HA_CA_FILE=/certs/ca.pem", "-e", "HM_HA_BROWSER_URL="+env.haURL,
		"-e", "SSL_CERT_FILE=/certs/ca.pem", // the stand-ins' CA, for the test run only
		"-e", "HM_TLS_CERT=/certs/ct-gw-cert.pem", "-e", "HM_TLS_KEY=/certs/ct-gw-key.pem",
		"-e", "HM_LOG_LEVEL=debug", "-e", "HM_PUBLIC_URL=https://"+ctName+":"+port, "-e", "HM_APPROVAL_TIMEOUT=180",
		env.image)...); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log(logsOf(g.name))
		}
		_, _ = run("rm", "-f", g.name)
	})
	// Before the first round (30 s after the start): the approver with the phone of the test.
	eventually(t, "approver added", 20*time.Second, func() bool {
		_, err := runInput("", "exec", "-i", g.name, "/home-mandate", "approver", "add", env.users[adminApprover].id, approverDevice)
		return err == nil
	})

	ingress, err := buildTool("ingressproxy")
	if err != nil {
		t.Fatal(err)
	}
	name := "hm-e2e-ct-ingress-" + env.id
	token := randomHex(32)
	g.uiPath = "/api/hassio_ingress/" + token
	if _, err := run("run", "-d", "--name", name, "--network", env.network, "--ip", ctIngressIP, "-p", "127.0.0.1::8080",
		"-v", env.certs+":/certs:ro", ingress, "-listen", ":8080", "-target", "http://"+g.name+":8099",
		"-ha", "https://homeassistant:8123", "-ca", "/certs/ca.pem", "-token", token); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = run("rm", "-f", name) })
	addr, err := hostPort(name, "8080")
	if err != nil {
		t.Fatal(err)
	}
	g.uiURL = "http://" + addr
	if err := waitHTTP(g.uiURL+"/", time.Minute); err != nil {
		t.Fatal(err)
	}
	return g
}

type ctSystem struct {
	CT struct {
		State        string `json:"state"`
		Name         string `json:"name"`
		OwnKeyKnown  bool   `json:"own_key_known"`
		LastSuccess  string `json:"last_success"`
		Certificates []struct {
			Digest       string `json:"digest"`
			Result       string `json:"result"`
			Acknowledged *struct {
				By  string `json:"by"`
				Via string `json:"via"`
			} `json:"acknowledged"`
		} `json:"certificates"`
	} `json:"ct"`
}

// bellShows tells whether Home Assistant shows the persistent notification id.
func bellShows(t *testing.T, id string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := dialHA(ctx, env.haToken)
	if err != nil {
		t.Fatal(err)
	}
	defer c.conn.CloseNow()
	res, err := c.command(ctx, map[string]any{"type": "persistent_notification/get"})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := res.([]any)
	for _, n := range list {
		if m, _ := n.(map[string]any); m["notification_id"] == id {
			return true
		}
	}
	return false
}

func TestCTMonitoring(t *testing.T) {
	notes := watchNotifications(t)
	g := startCTGateway(t)
	ui := uiLoginVia(t, adminApprover, g.uiURL, g.uiPath)
	var sys ctSystem
	// The first round runs 30 s after the start: one inventory with the foreign certificate.
	eventually(t, "foreign certificate found", 2*time.Minute, func() bool {
		ui.ok(http.MethodGet, "api/system", nil, &sys)
		return len(sys.CT.Certificates) > 0
	})
	if sys.CT.State != "active" || sys.CT.Name != ctName || !sys.CT.OwnKeyKnown || sys.CT.LastSuccess == "" {
		t.Fatalf("monitoring %+v", sys.CT)
	}
	if len(sys.CT.Certificates) != 1 || sys.CT.Certificates[0].Digest != g.foreignDigest.String() || sys.CT.Certificates[0].Result != "alarm" {
		t.Fatalf("certificates %+v; the gateway's own is %s", sys.CT.Certificates, g.ownDigest)
	}
	// The gateway also tells its new approver the state of its audit log; that is skipped.
	inventory := nextNotificationWhere(t, notes, func(n notification) bool { return strings.Contains(n.Title, ctName) })
	if inventory.Service != approverDevice || !strings.Contains(inventory.Title, ctName) || len(inventory.Actions) != 0 {
		t.Fatalf("inventory %+v", inventory)
	}
	eventually(t, "bell rung", 15*time.Second, func() bool { return bellShows(t, "hm_ct_alarm") })

	// "This is mine": confirmed on the phone, like a critical request.
	ui.ok(http.MethodPost, "api/ct/acknowledge", map[string]any{"digest": g.foreignDigest.String()}, nil)
	request := nextNotificationWhere(t, notes, func(n notification) bool {
		return len(n.Actions) > 0 && strings.HasPrefix(n.Actions[0], "HM_CT_")
	})
	if request.Service != approverDevice || len(request.Actions) != 2 || !strings.HasPrefix(request.Actions[0], "HM_CT_YES_") {
		t.Fatalf("confirmation %+v", request)
	}
	answer(t, adminApprover, request.Actions[0])
	eventually(t, "acknowledged", 30*time.Second, func() bool {
		ui.ok(http.MethodGet, "api/system", nil, &sys)
		a := sys.CT.Certificates[0].Acknowledged
		return a != nil && a.Via == "push" && a.By == env.users[adminApprover].id && sys.CT.Certificates[0].Result == "none"
	})
	eventually(t, "bell taken back", 30*time.Second, func() bool { return !bellShows(t, "hm_ct_alarm") })
	if !strings.Contains(logsOf(g.name), "certificate acknowledged as the household's") {
		t.Error("acknowledgement not logged")
	}
}
