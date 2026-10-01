# Home-Mandate

Mandate für KI-Agenten in Home Assistant: Jeder Agent bekommt eine eigene Identität und klare
Grenzen. Aktionen werden erlaubt, zur Bestätigung aufs Handy geschickt oder verboten, und jede
Anfrage wird protokolliert.

**Status:** in Entwicklung, Release v0.1 geplant für den 31.10.2026.

## Aufbau dieses Repositorys

| Pfad | Inhalt |
|---|---|
| `docs/ARCHITECTURE.md` | Architektur v0.1: Gateway, lokale Oberfläche, Abläufe, offene Entscheidungen |
| `docs/TASKS-v0.1.md` | Wochenplan bis zum Release, Schnittlinien |
| `docs/TESTING.md` | Teststrategie: Unit, Negativ, Fuzzing, E2E, Oberfläche, i18n, Abdeckungsschwellen |
| `SECURITY.md` | Meldung von Sicherheitslücken, Bedrohungsmodell |
| `app/config.yaml` | Entwurf der Home-Assistant-App-Konfiguration |
| `Dockerfile` | Mehrstufiger Build: Oberfläche (Vite) → Go-Binary mit eingebetteter Oberfläche |

Geplante Code-Struktur: `cmd/home-mandate` (Gateway), `cmd/relay` (Cloud-Relay, ab 2027),
`internal/…` (siehe Architektur), `web/` (lokale Oberfläche, Svelte + Vite), `e2e/`.

## Zugehörige Repositorys

| Repository | Inhalt | Lizenz |
|---|---|---|
| `mandate-spec` | Herstellerneutrale Spezifikation, Schema, Konformitätsfälle, Referenz-Auswertung, Prüfwerkzeug | CC BY 4.0 / Apache 2.0 |
| `home-mandate` (dieses) | Gateway, lokale Oberfläche, Home-Assistant-App, Relay | AGPL-3.0 |
| `home-mandate-cloud` (privat) | Portal, Webseite, Abrechnung, Betrieb | proprietär |

Home-Mandate bindet die Referenz-Auswertung aus `mandate-spec` als Go-Modul ein und muss alle
Konformitätsfälle bestehen.
