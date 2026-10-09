# Changelog

## 0.1.0-rc.1

First release candidate. Experimental.

- Mandates with allow, ask and deny per device, area, category and action; time windows
  and weekdays, value limits (brightness, temperature, position, volume), a rate limit per
  hour, validity; versions with comparison and restore
- Critical actions (unlocking, opening gates and garage doors, disarming the alarm, running
  scripts, activating scenes) and devices marked as critical need an approval unless a rule
  explicitly allows them without one
- Covers of the class garage, gate or door, and covers without a class (groups, template
  covers), count as gates: opening them is critical. Blinds, shades and windows stay covers
- Three built-in mandate templates and templates of your own
- Approval requests through the Home Assistant Companion app, optionally also in the UI;
  cooldown after a refusal, at most two waiting requests per agent
- Emergency stop and revoking single agents, effective with the next request
- Audit log with hash chain, signed checkpoints and a daily checkpoint notification to the
  approvers; 30 days retention
- Agents over MCP with OAuth 2.1: browser sign-in (Authorization Code with PKCE, Client ID
  Metadata Documents) or a pairing code; admitted only by Home Assistant administrators
- UI in Home Assistant's sidebar (Ingress), for administrators only; English and German
- Command line administration
- Container mode next to Home Assistant Container, without root, with the UI signed in
  through Home Assistant and optionally behind a TLS-ending reverse proxy
- Multi-architecture image (amd64, aarch64), signed with cosign, with provenance and SBOMs
