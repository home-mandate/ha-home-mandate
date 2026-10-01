# Sicherheit

## Sicherheitslücken melden

Bitte **nicht** über öffentliche Issues. Meldung an: security@home-mandate.com
(bis die Adresse steht: privat über GitHub Security Advisories dieses Repositorys).
Wir bestätigen innerhalb von 72 Stunden und veröffentlichen koordiniert.

## Bedrohungsmodell v0.1

### Schützenswert
- Physische Sicherheit des Hauses (Schlösser, Tore, Alarmanlage)
- Privatsphäre (Kameras, Anwesenheit, Gewohnheiten)
- Der Zugang von Home-Mandate zu Home Assistant (praktisch Vollzugriff)
- Agenten-Token und Mandate

### Angreifer und Gegenmaßnahmen

| Angreifer / Szenario | Gegenmaßnahme |
|---|---|
| **Manipulierter Agent** (Prompt-Injection über Webseite, E-Mail, Dokument) versucht Tür zu öffnen | Entscheidung außerhalb des Modells; Schlösser ab Werk `ask`; Grund des Agenten in Rückfragen als ungeprüft gekennzeichnet; Tempolimit gegen Schleifen |
| Agent versucht, eigene Rechte zu erweitern | Verwaltungsfunktionen sind über MCP nicht erreichbar; Mandate nur über UI durch HA-Admins änderbar |
| Agent erkundet Geräte außerhalb seines Mandats | Nicht lesbare Geräte werden weder gelistet noch in Fehlermeldungen erwähnt |
| Gestohlenes Agenten-Token | Zugriffstoken 10 Minuten; Refresh-Rotation mit Wiederverwendungserkennung; sofortiger Entzug; Not-Aus |
| Angreifer im LAN liest mit | TLS 1.3 mit hybrider Post-Quanten-Schlüsselvereinbarung; kein Klartext außerhalb von `localhost` |
| Gefälschte Freigabe (Event von anderer Stelle) | Nonce mit 128 Bit, einmalig; `context.user_id` muss Freigebender sein; iOS verlangt Entsperren |
| Wer bereits einen HA-Admin-Token hat | Außerhalb unseres Einflusses: könnte Geräte ohnehin direkt steuern. Dokumentation rät, Agenten nie direkt HA-Token zu geben |
| Kompromittierte Lieferkette | Reproduzierbare Builds, signierte Images, SBOM, minimale Abhängigkeiten, `govulncheck` in CI |
| Weitergegebene Datenbank (Backup, Support) | Token nur als Hash; HA-Zugang verschlüsselt mit separatem Schlüssel |

### Bewusst nicht abgedeckt in v0.1
- Kompromittierter Home-Assistant-Host (dann ist alles verloren)
- Angreifer mit Root auf dem Host
- Cloud-Szenarien (kommen mit Plus, eigenes Bedrohungsmodell)

## Regeln für Beiträge

- Niemals Token, Nonces, HA-Zugangsdaten oder vollständige Mandate ins Log schreiben.
- Jede neue Funktion, die Aktionen auslöst, geht durch den PDP. Keine Abkürzungen.
- Standard ist `deny`. Wer etwas erlaubt, muss es ausdrücklich tun.
- Neue Abhängigkeiten nur nach Begründung im Pull Request.
