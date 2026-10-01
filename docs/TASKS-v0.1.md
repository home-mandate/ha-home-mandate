# Aufgaben v0.1 – Release 31.10.2026

Arbeitsweise: jede Woche endet mit etwas Lauffähigem im eigenen Haus. Tests zuerst, wo es um
Entscheidungen geht. Ein Kästchen ist erst fertig, wenn Unit-, Negativ- und betroffene
E2E-Tests grün sind und `govulncheck` sowie `pnpm audit` nichts melden.

Zwei Repositorys: **[S]** = `mandate-spec`, **[H]** = `home-mandate`.

## Woche 1 · 29.09.–05.10. · Fundament und Referenz-Auswertung

- [ ] [S] Go-Modul anlegen; Wurzelpaket bettet `schema/`, `examples/`, `conformance/` per `embed` ein
- [ ] [S] `evaluator`: Datenmodell, Schema-Validierung, Auswertung gemäß `SPEC-v0.md` Abschnitt 4, nur Standardbibliothek plus Schema-Validator
- [ ] [S] Alle Konformitätsfälle als Go-Test; Fuzz-Test (nie Panik, Unbekanntes → `deny`); Mutationstests vorbereiten
- [ ] [S] Tag `v0.1.0-alpha.1`
- [ ] [H] Repository, Go-Modul, `go.work` für die lokale Entwicklung mit [S]
- [ ] [H] CI: `go vet`, `staticcheck`, `go test -race`, `govulncheck`, Abdeckungsschwellen aus `docs/TESTING.md`, Build amd64/aarch64
- [ ] [H] `web/`: Svelte 5 + Vite 8 + TypeScript, Paraglide mit `de` und `en`, Vitest, Playwright; CI-Schritte für Lint, Typprüfung, Tests, i18n-Vollständigkeit
- [ ] [H] `internal/store`: SQLite (reines Go), Migrationen
- [ ] [H] `internal/ha`: WebSocket-Verbindung, Authentifizierung, `get_states`, Registry-Abfragen, Wiederverbindung
- [ ] **Offene Entscheidungen 1–5** aus `docs/ARCHITECTURE.md` klären und dort festhalten
- [ ] Claude Design: erste Entwürfe für Agentenliste, Mandats-Editor, Protokoll, Rückfrage-Ansicht; Design-Tokens festlegen

## Woche 2 · 06.–12.10. · Entscheidung und Durchsetzung

- [ ] [H] `internal/catalog`: HA-Entitäten → Kategorie, Bereich, Aktionen (Spec Abschnitt 5), inklusive `gate` über device_class
- [ ] [H] `internal/mandate`: Speichern, Versionieren, Validieren; Auswertung über `mandate-spec/evaluator`
- [ ] [H] `internal/pdp`: AuthZEN-Endpunkt, nur intern gebunden, `ask` im Antwortkontext; alle Konformitätsfälle laufen zusätzlich gegen diesen Endpunkt
- [ ] [H] `internal/mcp`: `list_devices`, `get_state`, `perform_action`, `list_my_permissions`
- [ ] [H] PEP-Pfad: Token → Tempolimit → Katalog → PDP → Ausführung → Protokoll
- [ ] [H] `internal/ratelimit`, `internal/audit` (hash-verkettet, Kettenprüfung, 30 Tage)
- [ ] [H] E2E-Umgebung gemäß `docs/TESTING.md` Abschnitt 3; E2E-Szenarien 1, 5, 8, 12 grün; Negativtests MCP-Schnittstelle

## Woche 3 · 13.–19.10. · Agenten-Anmeldung und Rückfragen

- [ ] [H] `internal/oauth`: Metadaten (RFC 8414, RFC 9728), Authorization Code + PKCE, Resource Indicators, CIMD, Anmeldung des Menschen über das HA-Konto, nur Admins
- [ ] [H] Device Authorization Grant (RFC 8628) als Kopplungscode
- [ ] [H] Token: 256 Bit, nur Hash gespeichert, Zugriff 10 min, Refresh 30 Tage mit Rotation und Wiederverwendungserkennung; Entzug und Not-Aus
- [ ] [H] `internal/approval`: Aktions-Benachrichtigung, Nonce 128 Bit, `context.user_id`, Timeout → `deny`, iOS `authenticationRequired`
- [ ] [H] `internal/i18n`: Rückfrage-Texte und Server-Fehlermeldungen in `de` und `en`
- [ ] [H] Negativtests Token, Anmeldung, Rückfragen vollständig; E2E-Szenarien 2, 3, 4, 6, 7, 10 grün

## Woche 4 · 20.–26.10. · Oberfläche und Paketierung

- [ ] [H] `internal/api` + `internal/webui`: JSON-API, CSRF, strikte CSP, Ingress nur von 172.30.32.2
- [ ] [H] `web/`: Agenten (hinzufügen per Code, entziehen), Mandats-Editor mit Vorschau „darf danach“, Protokoll, Einstellungen, Not-Aus; alles aus den Claude-Design-Entwürfen
- [ ] [H] Sichere Voreinstellungen im Editor; `allow_critical` nur mit gesonderter Bestätigung
- [ ] [H] l10n: Haushalts-Zeitzone, HA-Einheiten, `Intl`-Formatierung; Pseudo-Lokalisierung ohne abgeschnittene Texte
- [ ] [H] App-Paket: `app/config.yaml`, mehrstufiges Dockerfile, eigenes App-Repository; TLS für MCP gemäß Entscheidung 1
- [ ] [H] Playwright: Oberfläche in `de` und `en` inklusive Negativtests; E2E-Szenarien 9, 11 grün
- [ ] [H] Installation auf der eigenen HA-OS-Instanz

## Woche 5 · 27.–31.10. · Härtung und Release

- [ ] Eigener Einsatz mit Claude Code und einem lokalen Modell, Protokoll einer Woche prüfen
- [ ] Sicherheits-Review entlang `SECURITY.md`: jede Zeile hat einen Test
- [ ] Mutationstests `mandate-spec/evaluator` ≥ 90 %; Fuzzing ohne Befund
- [ ] Alle 12 E2E-Szenarien gegen das Release-Image grün; Log-Durchsuchung nach Geheimnissen ohne Treffer
- [ ] Reproduzierbarer Build, signiertes Image, SBOM (Go und JavaScript)
- [ ] README in `de` und `en`: Installation, erstes Mandat in 5 Minuten, Grenzen von v0.1
- [ ] Lizenzdateien: [H] AGPL-3.0; [S] CC BY 4.0 für die Spezifikation, Apache-2.0 für alles andere
- [ ] [S] Tag `v0.1.0`; [H] Release `v0.1.0` am 31.10.

## Schnittlinien, falls die Zeit nicht reicht

In dieser Reihenfolge nach v0.2 verschieben, **nie** an Sicherheit oder Tests sparen:

1. Container-Modus (nur App-Modus für v0.1)
2. Device Authorization Grant (nur Authorization Code)
3. Vorschau „darf danach“ im Mandats-Editor
4. Feinschliff der Oberfläche über die Claude-Design-Entwürfe hinaus

Nicht verhandelbar für v0.1: Standard `deny`, Rückfragen für kritische Aktionen,
Token-Handling, Not-Aus, Konformitätstests, Negativtests aus `docs/TESTING.md`,
Abdeckungsschwellen, E2E-Szenarien 2–7, Oberfläche in Deutsch und Englisch.
