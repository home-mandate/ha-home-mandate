# Teststrategie

Home-Mandate kann Türen öffnen. Deshalb gilt: **Kein Code ohne Test, keine Sicherheitsfunktion
ohne Negativtest, kein Release ohne grüne Ende-zu-Ende-Tests.**

## 1. Ebenen

| Ebene | Werkzeug | Läuft | Zweck |
|---|---|---|---|
| Unit | `go test`, tabellengetrieben | jeder Commit | Jede Funktion, jeder Zweig |
| Konformität | Fälle aus `mandate-spec` (eingebettet im Go-Modul) gegen den eigenen PDP | jeder Commit | Auswertung exakt nach Spezifikation |
| Negativ | eigene Testfälle je Paket, Katalog in Abschnitt 4 | jeder Commit | Angriffe und Fehleingaben werden abgewiesen |
| Fuzzing | `go test -fuzz` | nächtlich, 10 min je Ziel | Keine Panik, Unbekanntes wird `deny` |
| Integration | echte HA-Instanz im Container | jeder Push | HA-Client, Katalog, Service-Aufrufe |
| Ende zu Ende | MCP-Client + OAuth + HA-Container, UI mit Playwright | jeder Push, vor jedem Release | Komplette Abläufe aus Sicht von Agent und Mensch |
| Oberfläche Unit | Vitest + Svelte Testing Library | jeder Commit | Komponenten, Formularlogik, Formatierung |
| i18n | eigene Prüfschritte in CI (Abschnitt 5) | jeder Commit | Keine fehlenden, verwaisten oder fehlerhaften Übersetzungen |
| Mutation | Mutationstests auf `mandate-spec/evaluator` | vor jedem Release | Tests erkennen absichtlich eingebaute Fehler |

Alle Go-Tests laufen mit `-race`.

## 2. Abdeckungsschwellen (CI bricht ab, wenn unterschritten)

| Bereich | Zeilenabdeckung |
|---|---|
| `mandate-spec/evaluator`, `internal/pdp`, `internal/oauth`, `internal/approval`, `internal/audit`, `internal/api` | ≥ 95 % |
| übrige Go-Pakete | ≥ 85 % |
| `web/src/lib` (Logik, ohne reine Darstellung) | ≥ 85 % |
| Mutationswert `mandate-spec/evaluator` | ≥ 90 % getötete Mutanten |

Abdeckung ist eine Untergrenze, kein Ziel. Jeder Entscheidungszweig (`allow`, `ask`, `deny`,
Schutzklasse, abgelaufen, noch nicht gültig) braucht einen eigenen benannten Test.

## 3. Ende-zu-Ende-Umgebung

- **Home Assistant** als Container (offizielles Image, feste Version) mit vorbereiteter
  Konfiguration. Die eingebaute `demo`-Integration liefert Lichter, Schlösser, Kameras,
  Alarmanlage und Klima ohne echte Hardware.
- **Testbenutzer** in HA: `admin-approver` (Admin, Freigebender), `admin-other` (Admin, kein
  Freigebender), `user-plain` (kein Admin).
- **Home-Mandate** als Container aus dem Release-Image, nicht aus dem Quellcode, damit das
  ausgelieferte Artefakt getestet wird.
- **Agent** = Test-Client auf Basis des offiziellen MCP-Go-SDK, durchläuft echte OAuth-Flüsse.
- **Rückfragen**: Versand gegen einen konfigurierbaren `notify`-Dienst; die Antwort wird als
  Event `mobile_app_notification_action` über die HA-API mit dem jeweiligen Testbenutzer
  ausgelöst, sodass `context.user_id` echt gesetzt ist.
- **Oberfläche**: Playwright gegen die UI (Mandats-Editor, Not-Aus, Agent entziehen),
  jedes UI-Szenario in Deutsch **und** Englisch, eingebettet unter einem zufälligen
  Ingress-Pfad, damit relative Pfade und Hash-Routing geprüft werden.
- Start und Abbau per `docker compose` bzw. Podman; jeder Testlauf mit frischem Zustand.

### Pflichtszenarien E2E

1. Agent koppeln per Code, Mandat „Sprachassistent“ wählen, Licht schalten → ausgeführt, protokolliert.
2. Tür öffnen → Rückfrage → `admin-approver` bestätigt → ausgeführt.
3. Tür öffnen → Rückfrage → keine Antwort → nach Timeout abgelehnt, Tür bleibt zu.
4. Tür öffnen → Antwort von `admin-other` → verworfen, abgelehnt, Protokolleintrag „ungültige Freigabe“.
5. Kamera abrufen → abgelehnt, Kamera taucht in `list_devices` nicht auf.
6. Agent entziehen in der UI → nächste Anfrage mit altem Token abgelehnt.
7. Not-Aus → alle Agenten sofort gesperrt; Aufheben → nur neu ausgestellte Token funktionieren.
8. Tempolimit überschreiten → Absage ab Anfrage n+1, Protokoll.
9. Mandat mit Zeitfenster: Anfrage außerhalb (Testuhr) → abgelehnt.
10. `user-plain` versucht, einen Agenten zuzulassen → abgelehnt.
11. Neustart von Home-Mandate → Mandate, Agenten und Protokoll unverändert, Protokollkette gültig.
12. HA nicht erreichbar → Anfragen abgelehnt mit klarem Fehler, keine Warteschlange, die später ausführt.

## 4. Katalog der Negativtests

Jede Zeile ist mindestens ein Test. Neue Angriffsideen werden hier ergänzt, bevor sie
behoben werden.

**Mandat und Auswertung**
- Mandat verletzt Schema (unbekannte Felder, `default: allow`, `any` mit weiteren Feldern) → abgelehnt beim Speichern
- Aktion passt nicht zur Kategorie (`unlock` auf `light`) → abgelehnt beim Speichern
- Unbekannte Kategorie oder Entität zur Laufzeit → `deny`
- Leeres Mandat, Mandat ohne Regeln → alles `deny`
- Zeitfenster über Mitternacht, Grenzwerte 00:00 und 23:59, Zeitumstellung (25.10.2026) → korrekt
- Kritische Aktion mit `allow` ohne `allow_critical` → `ask`

**Token und Anmeldung**
- Kein Token, falsches Schema, abgelaufen, widerrufen, für andere Ressource ausgestellt → 401
- Refresh-Token zweimal benutzt → ganze Kette widerrufen
- PKCE fehlt oder falscher Verifier → abgelehnt
- Redirect-URI weicht ab (auch nur Groß-/Kleinschreibung, Pfadzusatz) → abgelehnt
- Client-Metadaten nicht erreichbar, falsches Format, Client-ID ≠ URL → abgelehnt
- Kopplungscode falsch, abgelaufen, mehrfach benutzt, Brute-Force → gesperrt nach n Versuchen
- Zulassung durch Nicht-Admin → abgelehnt

**Rückfragen**
- Antwort mit unbekannter, abgelaufener oder bereits benutzter Nonce → verworfen
- Antwort von Nicht-Freigebendem → verworfen
- Gleichzeitig „Ja“ und „Nein“ → erste gültige Antwort zählt, zweite verworfen, beides protokolliert
- Sehr langer oder manipulierter „Grund“ des Agenten (Steuerzeichen, Markdown, Links) → gekürzt, bereinigt, als Angabe des Agenten gekennzeichnet

**MCP-Schnittstelle**
- Unbekanntes Werkzeug, fehlende oder zusätzliche Parameter, falsche Typen → Fehler ohne interne Details
- Entität außerhalb des Mandats in `get_state` → identische Antwort wie bei nicht existierender Entität
- Übergroße Anfragen, tief verschachteltes JSON → abgelehnt
- Versuch, Verwaltungsfunktionen über MCP zu erreichen → nicht vorhanden

**Oberfläche**
- Anfrage ohne CSRF-Token → abgelehnt
- Ingress-Anfrage von anderer Quelle als 172.30.32.2 → abgelehnt
- Eingaben mit HTML/Skript → korrekt maskiert (Playwright prüft das Rendering)
- Content-Security-Policy: Playwright meldet jede CSP-Verletzung als Testfehler
- Build enthält keine Verweise auf externe Hosts (Prüfung des `dist/`-Verzeichnisses)
- API-Aufruf ohne gültige Ingress-Sitzung bzw. als Nicht-Admin → abgelehnt

**Protokoll**
- Manipulierter Eintrag in der Datenbank → Kettenprüfung schlägt fehl und meldet die Stelle
- Keine Token, Nonces oder HA-Zugangsdaten in Logs (Test durchsucht Logausgaben aller E2E-Läufe)

**Transport**
- TLS 1.2 oder älter → Verbindung abgelehnt
- Handshake mit `X25519MLKEM768` wird ausgehandelt, wenn der Client es anbietet

## 5. Internationalisierung prüfen

Jeder Commit prüft automatisch:
- **Vollständigkeit:** jeder Schlüssel existiert in `de` und `en`; keine verwaisten Schlüssel.
- **Platzhalter:** gleiche Variablen in allen Sprachen, gültige Plural-Varianten.
- **Keine festen Texte:** Lint-Regel gegen sichtbare Zeichenketten in Svelte-Komponenten
  außerhalb der Nachrichtenkataloge.
- **Pseudo-Lokalisierung:** ein künstliches Gebietsschema mit um 40 % verlängerten Texten und
  Sonderzeichen; Playwright prüft, dass nichts abgeschnitten wird oder überläuft.
- **Formatierung:** Unit-Tests für Datum, Uhrzeit, Zahlen und relative Zeiten in `de-DE` und
  `en-US`, jeweils mit Haushalts-Zeitzone, die von der Zeitzone des Testrechners abweicht;
  Grenzfall Zeitumstellung am 25.10.2026.
- **Server-Texte:** Rückfrage-Benachrichtigungen werden in beiden Sprachen gerendert und gegen
  gespeicherte Referenzen verglichen.

## 6. Regeln

- Ein Fehler wird zuerst als fehlschlagender Test nachgestellt, dann behoben.
- Tests prüfen Verhalten, nicht Implementierungsdetails. Keine Tests, die nur Mocks prüfen.
- Keine übersprungenen Tests im Hauptzweig. Instabile Tests werden repariert, nicht deaktiviert.
- Testdaten enthalten keine echten Zugangsdaten; Geheimnisse werden pro Lauf erzeugt.
