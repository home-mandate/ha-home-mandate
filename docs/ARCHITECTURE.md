# Home-Mandate – Architektur v0.1

Stand: 28.09.2026 · Ziel: Release v0.1 am 31.10.2026

## 1. Zweck

Home-Mandate sitzt zwischen KI-Agenten und Home Assistant. Jeder Agent bekommt eine eigene
Identität und ein **Mandat**: welche Geräte er mit welchen Aktionen nutzen darf und ob das
sofort erlaubt ist, eine Rückfrage beim Menschen braucht oder verboten ist. Jede Anfrage wird
protokolliert.

Grundsatz: **Die Entscheidung trifft ein festes Regelwerk außerhalb des Sprachmodells.**
Kein Agent kann seine Rechte lesen, ändern oder sich „herbeireden“.

## 2. Überblick

```
 KI-Agent (Claude Code, lokales LLM, n8n, OpenClaw …)
        │  MCP über HTTPS · OAuth-Token pro Agent
        ▼
 ┌──────────────────────────── Home-Mandate (ein Go-Binary) ────────────────────────────┐
 │                                                                                      │
 │  oauth ──► mcp (PEP) ──► pdp (AuthZEN-Endpunkt) ──► mandate store                    │
 │               │  allow           │ ask                                               │
 │               │                  ▼                                                   │
 │               │            approval ──► HA-Push an Freigebende ──► Antwort/Timeout   │
 │               ▼                                                                      │
 │            ha client ──► Home Assistant (WebSocket-API)                              │
 │                                                                                      │
 │  audit (hash-verkettet) · ratelimit · ui (Ingress bzw. eigener Port)                 │
 └──────────────────────────────────────────────────────────────────────────────────────┘
        │
        ▼
 Home Assistant ──► Geräte
```

PEP = Policy Enforcement Point (setzt durch), PDP = Policy Decision Point (entscheidet).
Beide laufen im selben Prozess, sprechen aber **über die AuthZEN-Schnittstelle** miteinander.
Dadurch können später andere Gateways (Paperless, Immich, Nextcloud) dieselbe
Entscheidungsstelle nutzen, ohne Umbau.

## 3. Komponenten (Go-Pakete)

| Paket | Aufgabe |
|---|---|
| `cmd/home-mandate` | Einstiegspunkt, Konfiguration, Start der Dienste |
| `internal/config` | Liest `/data/options.json` (App-Modus) bzw. Umgebungsvariablen (Container-Modus) |
| `internal/ha` | Client für die HA-WebSocket-API: Zustände, Entity-/Device-/Area-Registry, Service-Aufrufe, Benachrichtigungen, Event-Abo |
| `internal/catalog` | Bildet HA-Entitäten auf das Mandats-Vokabular ab (Gerätekategorie, Bereich, erlaubte Aktionen) |
| `internal/mandate` | Speichern, Versionieren und Validieren von Mandaten; die **Auswertung** kommt aus der Referenz-Bibliothek `mandate-spec/evaluator` (Go-Modul, eingebunden mit fester Version) |
| `internal/pdp` | AuthZEN-Endpunkt `POST /access/v1/evaluation`, nur intern gebunden |
| `internal/mcp` | MCP-Server (Streamable HTTP), eigene Werkzeuge, ruft vor jeder Aktion den PDP |
| `internal/oauth` | Autorisierungsserver für Agenten: Authorization Code + PKCE, Device Authorization Grant (Kopplungscode), Token-Verwaltung |
| `internal/approval` | Rückfragen per Aktions-Benachrichtigung, Warten auf Antwort, Timeout |
| `internal/audit` | Protokoll, hash-verkettet, 30 Tage Aufbewahrung |
| `internal/ratelimit` | Token-Bucket pro Agent |
| `internal/store` | SQLite (reines Go, kein CGO), Migrationen |
| `internal/api` | JSON-API für die Oberfläche (nur für angemeldete HA-Admins), CSRF-Schutz, strikte Eingabeprüfung |
| `internal/i18n` | Übersetzungen für Texte, die der Server erzeugt (Rückfrage-Benachrichtigungen, Fehlermeldungen in der UI) |
| `internal/webui` | Liefert die eingebettete Oberfläche aus (`embed.FS`) mit strikter Content-Security-Policy |
| `web/` | Lokale Oberfläche: Svelte 5 + Vite 8, wird beim Build ins Binary eingebettet (Abschnitt 12) |

## 4. MCP-Werkzeuge in v0.1

Home-Mandate bietet **eigene Werkzeuge** an und leitet nicht den MCP-Server von Home Assistant
durch. Grund: Nur so lässt sich jede Anfrage eindeutig auf Gerät und Aktion abbilden.

| Werkzeug | Aktion im Mandat | Zweck |
|---|---|---|
| `list_devices` | `read` | Listet nur Geräte, auf die der Agent mindestens Leserecht hat |
| `get_state` | `read` | Zustand eines Geräts |
| `perform_action` | je nach Kategorie, z. B. `turn_on`, `unlock` | Führt eine Aktion aus, nach Entscheidung |
| `list_my_permissions` | – | Zeigt dem Agenten, was er darf (hilft Modellen, unnötige Anfragen zu vermeiden). Zeigt nie die Regeln anderer Agenten. |

Geräte, auf die ein Agent keinen Lesezugriff hat, existieren für ihn nicht (kein Hinweis auf
ihre Existenz in Listen oder Fehlermeldungen).

## 5. Ablauf einer Anfrage

1. Agent ruft `perform_action(entity_id="lock.haustuer", action="unlock")` auf.
2. `mcp` prüft das Token (gültig, nicht widerrufen, für diese Ressource ausgestellt).
3. `ratelimit` prüft das Kontingent des Agenten. Überschreitung → Absage, Protokoll.
4. `catalog` löst die Entität auf: Kategorie `lock`, Bereich `flur`.
5. `mcp` stellt eine AuthZEN-Anfrage an den `pdp` (siehe `mandate-spec/SPEC-v0.md`, Abschnitt 6).
6. `pdp` wertet das Mandat aus. Ergebnis: `allow`, `ask` oder `deny`.
7. Bei `ask`: `approval` sendet eine Aktions-Benachrichtigung an alle Freigebenden und wartet
   höchstens bis zum Timeout (Standard 2 Minuten). Keine oder negative Antwort → `deny`.
8. Bei `allow`: `ha` ruft den Service auf.
9. `audit` schreibt einen Eintrag mit Agent, Aktion, Ressource, Entscheidung, Grund, Dauer.
10. Agent erhält Ergebnis bzw. eine klare Absage mit Begründungscode, ohne interne Details.

## 6. Onboarding von Agenten

Zwei Wege, beide Standard-OAuth, beide verlangen eine Bestätigung im Haus:

**a) Browser-fähige Agenten** (Authorization Code + PKCE, OAuth 2.1)
- Agent entdeckt Home-Mandate über Protected Resource Metadata (RFC 9728) und
  Authorization Server Metadata (RFC 8414), wie in der MCP-Spezifikation vorgesehen.
- Client-Identifikation über Client ID Metadata Documents (CIMD). Offene dynamische
  Registrierung ist **aus**.
- Der Mensch meldet sich mit seinem Home-Assistant-Konto an (HA als Identitätsanbieter über
  dessen OAuth für externe Anwendungen) und wählt das Mandat für den neuen Agenten.
- Nur HA-Administratoren dürfen Agenten zulassen.

**b) Agenten ohne Browser** (Device Authorization Grant, RFC 8628) – der „Kopplungscode“
- Agent fordert einen Code an und zeigt ihn an.
- Mensch gibt den Code in der Home-Mandate-Oberfläche ein und wählt das Mandat.

**Token:** undurchsichtige Zufallswerte (256 Bit), in der Datenbank nur als Hash gespeichert.
Zugriffstoken 10 Minuten, Refresh-Token 30 Tage mit Rotation und Wiederverwendungserkennung
(Wiederverwendung eines alten Refresh-Tokens widerruft die ganze Kette). Token sind an die
Ressource Home-Mandate gebunden (Resource Indicators, RFC 8707).
Entzug eines Agenten oder Not-Aus wirkt sofort, weil jedes Token bei jeder Anfrage geprüft wird.

## 7. Rückfragen („ask“)

- Versand über `notify.mobile_app_<gerät>` an die in den Einstellungen gewählten Freigebenden.
- Aktionskennungen enthalten eine zufällige Nonce (128 Bit): `HM_APPROVE_<nonce>`, `HM_DENY_<nonce>`.
- Auswertung des Events `mobile_app_notification_action`: Nonce muss offen sein, `context.user_id`
  muss zu einem Freigebenden gehören, sonst wird die Antwort verworfen und protokolliert.
- Auf iOS wird `authenticationRequired: true` gesetzt (Entsperren nötig).
- Der vom Agenten gelieferte „Grund“ wird in der Nachricht ausdrücklich als Angabe des
  Agenten gekennzeichnet, nicht als Tatsache.
- Timeout (Standard 2 Minuten) → `deny`. Jede Nonce ist nur einmal gültig.

## 8. Betriebsarten

| | App-Modus (Home Assistant OS) | Container-Modus (Home Assistant Container) |
|---|---|---|
| Start | App aus eigenem Repository | `docker run` / Podman-Quadlet mit demselben Image |
| Zugang zu HA | `SUPERVISOR_TOKEN`, API über `http://supervisor/core/…` | Langzeit-Token eines eigenen HA-Benutzers, URL per Variable |
| Oberfläche | Ingress (Anmeldung durch HA) | eigener Port, Anmeldung mit HA-Konto (OAuth) |
| Daten | `/data` | gemountetes Volume |

Ein Image, zwei Konfigurationsquellen. Architekturen: `amd64`, `aarch64`.

## 9. Datenhaltung

SQLite unter `/data/home-mandate.db`:
`agents`, `mandates` (JSON gemäß Schema, versioniert), `tokens` (Hashes), `approvals`,
`audit` (hash-verkettet), `settings`.

Der HA-Zugang im Container-Modus wird mit einem Schlüssel verschlüsselt, der getrennt von der
Datenbank liegt (`/data/secret.key`, Dateirechte 0600). Das schützt vor dem versehentlichen
Weitergeben der Datenbank (Backup, Support-Anfrage), nicht vor einem Angreifer mit Vollzugriff
auf das Dateisystem. Das wird so dokumentiert.

## 10. Kryptografie

- TLS 1.3 als Minimum, Go-Standardkurven inklusive **X25519MLKEM768** (hybride
  Post-Quanten-Schlüsselvereinbarung, in Go seit 1.24 Standard). Build mit aktueller stabiler
  Go-Version.
- Signaturen (Audit-Kette, spätere signierte Entscheidungsbelege): vorerst Ed25519 hinter einer
  Schnittstelle, damit ML-DSA ohne Formatbruch nachgezogen werden kann, sobald `crypto/mldsa` in
  der eingesetzten Go-Version verfügbar ist.
- Zufallswerte ausschließlich aus `crypto/rand`.

## 11. Offene Entscheidungen (vor Woche 2 klären)

1. **TLS für den MCP-Endpunkt im LAN.** Viele Clients lehnen selbstsignierte Zertifikate ab.
   Vorschlag: vorhandenes Zertifikat aus `/ssl` nutzen (üblich bei HA-OS-Installationen mit
   Let's Encrypt/DuckDNS), sonst nur `localhost` ohne TLS. Kein Klartext im LAN.
2. **Rechte des HA-Benutzers im Container-Modus.** Prüfen, welche WebSocket-Befehle
   (Registry-Abfragen) Adminrechte verlangen. Falls nötig: Admin-Benutzer, dafür klar
   dokumentiert.
3. **Schreibrechte für Nicht-Root** auf `/data` im App-Modus prüfen; sonst als Root mit
   schreibgeschütztem Dateisystem und ohne zusätzliche Capabilities.
4. **App-Konfigurationsformat** gegen die aktuelle Entwicklerdoku prüfen (seit Supervisor
   2026.04 kein automatisches `BUILD_FROM` mehr).
5. **Sprache der Rückfrage-Benachrichtigungen.** Prüfen, ob die bevorzugte Sprache eines
   HA-Benutzers serverseitig abrufbar ist. Sonst: Sprache aus der HA-Systemkonfiguration,
   in den Home-Mandate-Einstellungen pro Freigebendem überschreibbar.

## 12. Lokale Oberfläche

**Technik:** Svelte 5 mit Vite 8, als Single-Page-App gebaut und per `embed.FS` in das
Go-Binary eingebettet. Im Haus läuft weiterhin genau ein Binary, keine Node-Laufzeit.

**Design:** Entwürfe entstehen mit Claude Design. Farben, Typografie und Abstände werden als
Design-Tokens (CSS-Variablen) in `web/src/lib/tokens/` übernommen und später mit Portal und
Webseite geteilt. Komponenten werden aus den Entwürfen in Svelte umgesetzt, nicht als
generierter Code übernommen.

**Besonderheiten durch Home Assistant Ingress:**
- Die Oberfläche läuft unter einem Pfad, den HA pro Installation vergibt. Deshalb Vite mit
  `base: './'` (relative Pfade) und Hash-Routing (`#/agents`, `#/mandates/…`), kein
  Router, der den Basispfad zur Build-Zeit kennen muss.
- Die UI spricht nur mit der eigenen JSON-API (`internal/api`) unter demselben Ursprung.
- Ingress-Anfragen werden nur von 172.30.32.2 angenommen.

**Sicherheit im Frontend:**
- Content-Security-Policy ohne `unsafe-inline` und ohne `unsafe-eval`; alle Skripte und
  Stile als Dateien aus dem Build.
- Keine externen Ressourcen (keine CDNs, keine Web-Fonts von Dritten); Schriften werden
  mitgeliefert.
- Abhängigkeiten minimal; `pnpm` mit Lockfile, Installations-Skripte von Paketen
  abgeschaltet, Updates mit Mindestalter, JavaScript-Abhängigkeiten in der SBOM.
- Svelte-Ausgaben werden standardmäßig maskiert; `{@html}` ist verboten.

### i18n und l10n

- **Bibliothek:** Paraglide JS (inlang), vom Svelte-CLI unterstützt. Übersetzungen werden
  zur Build-Zeit kompiliert, typsicher, nicht benutzte Texte fallen weg.
- **Sprachen zum Release:** Deutsch und Englisch. Ab v0.2 Community-Übersetzungen über
  Weblate (Open Source, in der EU betreibbar).
- **Formatierung** ausschließlich über die `Intl`-APIs des Browsers: Datum, Uhrzeit,
  relative Zeiten („vor 3 Minuten“), Zahlen, Pluralformen, Listen.
- **Zeitzone:** Zeiten werden immer in der Zeitzone des Haushalts aus der HA-Konfiguration
  angezeigt, nicht in der des Browsers. Zeitfenster in Mandaten sind Ortszeit des Haushalts.
- **Einheiten:** Temperatur und Maße aus dem HA-Einheitensystem (°C/°F).
- **Sprachwahl:** Reihenfolge: Einstellung des Benutzers in Home-Mandate → Browsersprache →
  Englisch als Rückfall.
- **Vorbereitet für weitere Schriften:** CSS nur mit logischen Eigenschaften
  (`margin-inline-start` statt `margin-left`), damit spätere rechts-nach-links-Sprachen
  ohne Umbau funktionieren.
- **Stabile Schlüssel:** Kategorien, Aktionen und Begründungscodes aus der Spezifikation
  bleiben englische Bezeichner; übersetzt werden nur ihre Anzeigenamen.
- **Server-Texte** (Rückfragen, Fehlermeldungen): eigene Kataloge in `internal/i18n`,
  gleiche Schlüsselkonventionen, ebenfalls über Weblate gepflegt.
