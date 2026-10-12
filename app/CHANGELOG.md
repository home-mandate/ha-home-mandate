# Changelog

## 0.1.0-rc.3

Second release candidate. Experimental.

Approval requests
- An approval request no longer depends on the agent's call: after a short wait (default
  45 s, `approval_wait_seconds`) the agent gets a "pending" result with an approval ID and
  asks again with `approval_status`; a confirmation given later is still executed, exactly
  once. `approval_cancel` withdraws a request. Clients that give up after a minute
  (Claude Desktop) no longer lose the answer
- A repeated request of the same agent for the same action is tied to the open one instead
  of asking again; shortly after a confirmed execution the same call is not executed again;
  optional `idempotency_key`
- The device's state when the request was made is shown in the notification and checked
  before the execution: an action whose target is already reached, or whose device changed
  meanwhile, is not executed
- Requests survive a restart only as a visible end: a request that was waiting is recorded
  as cancelled, never executed afterwards; the notification is replaced
- Notifications disappear from the phone when a request ends; revocation and emergency stop
  are recorded with their cause
- Refusals that protect the approvers say when the agent may ask again; no cooldown after a
  single unanswered request
- The hint in Home Assistant's notification bell points to where the UI really is (in
  container mode: a link to the UI)

Values and units
- A value the vocabulary's unit cannot express (e.g. volume 0.335, 21.555 °C) is refused
  with a clear message instead of being silently left out; what is executed is exactly what
  was checked
- An unknown temperature unit is never taken for degrees Celsius. In households that use
  °F, temperatures are refused until °F support comes in the next release candidate

UI
- Weekdays follow the first day of the week of the household's country; the country, time
  zone and units are shown under Settings → Home Assistant connection
- No more shifting layout: active entries, tabs and selected options are marked without bold
  text or thicker borders; unselected chips show an x
- "Review and save" instead of "Save …"; the preview legend explains its marks ("Asks
  because critical", "Time-dependent", "Within limits")
- The explanation of "critical requests too" sits where the switches are

Start-up
- Start-up errors are JSON log lines; the TLS file name options name the option that is
  wrong, and both empty means TLS off (MCP endpoint on localhost only)

## 0.1.0-rc.2

First published release candidate (0.1.0-rc.1 was tagged, but its release build stopped before publishing). Experimental.

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
