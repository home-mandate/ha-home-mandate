# Changelog

## 0.1.0-rc.10

Ninth release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.5, which changes no rule, format or conformance case of rc.4.

Conformance
- Both conformance reports attached to a release name Home-Mandate and the release, for
  example `0.1.0-rc.10`: before, the process report said `v0` and the HTTP report named
  nothing. A report without them now fails the check (#117)

Documentation
- Verifying the image's signature needs cosign 3 or later; cosign 2 does not find it (#117)

## 0.1.0-rc.9

Eighth release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.4.

Audit mode
- A deny rule in a mandate in audit mode no longer takes a confirmation away: if a
  matching rule asks, the request is not carried out at once but asked for, with the
  approval settings of that rule. The notification and the approval page say that the
  mandate would deny the request and by which rule; the agent sees only that approval
  is needed. Right before it is carried out the request is evaluated again, and anything
  stricter than before stops it. The audit log records such requests with `confirm`, and
  the entry says why approval was requested. A deny without a matching rule that asks is
  still carried out without asking, as before (#114)

Approvals
- Approval requests with a fractional value, for example 21.5 °C or a volume of 40 %, no
  longer fail before anyone is asked (#108)

Design
- New logo, a house with a key, in the UI, on the sign-in and consent pages, as favicon
  and as the app's icon and logo in Home Assistant. The name and the logo are not covered
  by the AGPL (#101)

Maintenance
- Every parser of input from agents, browsers, Home Assistant events and fetched metadata
  is fuzzed, briefly in every pull request and for an hour twice a month (#107)
- Dependencies of the CI and the container image are updated weekly in groups (#111)

## 0.1.0-rc.8

Seventh release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.3.

Approvals
- Every approval request has a page of its own, and a tap on the phone's notification
  opens it: one tap instead of five to a decision. The page shows what is to be
  confirmed with large buttons and, once the request is answered or has ended, only how
  it ended, never who answered. Anyone who is not an approver of the request sees only
  that they cannot answer it (#53)
- The hint in Home Assistant's notification bell links to that page. Every Home
  Assistant user can see the bell, so it now carries the request's random ID, nothing
  about what the request is about (#53)
- In a container with its own address, a link to a request signs in through Home
  Assistant if needed and then opens the request (#53)
- Nobody is asked for an action the device cannot carry out, for example "open" on a lock
  without that function: the agent is told right away what the device can do instead, and
  the audit log records "not executed" without a confirmation. The agent's list of
  permissions offers only what each device can do. Devices that report nothing are asked
  for as before (#89)

UI
- Saving works with Home Assistant reached over plain HTTP, for example
  `http://homeassistant.local:8123`: every change in the UI was refused there before (#94)

Maintenance
- The UI's tests use jsdom 30.1.3 (advisories in undici, which only the tests use)

## 0.1.0-rc.7

Sixth release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.3.

Mandates
- Areas and floors: a device has its room and, if the room is on a floor in Home
  Assistant, the floor (`floor:` and its ID). A rule can name a room or a whole floor (#80)
- Devices that leave or enter a room or floor a rule names are listed in the mandate list
  with the mandates they affect, until someone marks them as seen (#80)
- The climate mode can be limited: a rule on "set mode" lets through only the checked
  modes. "Automatic" stands for Home Assistant's `heat_cool` and `auto` (#81)
- Audit mode (trial): a mandate can be tried before it enforces its denials. What it would
  deny is carried out and logged as "not enforced"; critical actions, confirmations, the
  rate limit and the emergency stop still apply, and the agent does not notice. Switching
  it on takes effect only after someone in the household confirms that version; the
  approvers are notified when it is proposed and when it takes effect (#82)
- The editor says when limits need a rule of their own (an allowing rule whose actions all
  carry the value, e.g. only "set mode")

Agents
- Numbers are taken exactly as the agent sends them: a value that is no whole step of its
  unit is refused, also where floating point would round it (#81)
- `list_devices` gives each device its `areas` (room and floor) and lists floors; the
  `area` filter takes a floor as well. `list_my_permissions` names permitted modes (#80, #81)

Audit log
- Decision entries name the set of areas and the evaluated values; the details show them
  ("Mode: Cool", "Temperature: 21.5 °C"). The log can be filtered by "not enforced"
- Upgrade: a log written by an earlier version whose entries name a single area no longer
  matches the audit format of the specification. On the first start it is archived
  unchanged as `audit-archive-<time>.jsonl` in the data directory and the log is truncated
  with a signed checkpoint, only if its chain and checkpoints are intact

## 0.1.0-rc.6

Fifth release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.1.

Security
- Built with Go 1.27.3, which fixes GO-2026-6809: the certificate check of Go 1.27.2 still
  trusted some compromised intermediate certificates. Home-Mandate reaches it through its
  HTTPS server

Maintenance
- The UI is built with vite 8.3.3 (advisories in launch-editor, which only the
  development server uses; the published UI is not affected)
- An end-to-end test that failed now and then is fixed

## 0.1.0-rc.5

Fourth release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.1.

Agents
- ChatGPT can connect: its client metadata declares `private_key_jwt` and lists `none`
  among the methods it supports, and such a client is now admitted as a public client
  like every other (#70). A token request that carries client authentication anyway is
  refused with `invalid_client`
- A client that presents the same refresh token twice within 10 seconds, after a retry or
  from several workers at once, gets another pair instead of having all its tokens revoked
  (#71); later reuse still revokes them

## 0.1.0-rc.4

Third release candidate. Experimental. Implements the Home-Mandate Specification
v0.1.0-rc.1.

Agents
- `list_devices` and `list_my_permissions` filter by area, category and a search text and
  come in pages of 100 (up to 500) with `total` and `next_cursor`; the first unfiltered page
  of `list_devices` lists areas and categories with their counts. `list_my_permissions`
  groups devices with the same permissions (`groups` instead of `devices`): clients that
  cached the old tool schema reload the tools once
- An agent learns its own limits: `list_my_permissions` lists an action allowed only within
  limits (it used to be missing) with them, and a refusal outside them names them
- `get_state` gives a climate device's `temperature_unit`; the tools explain that its state
  is the operating mode and `hvac_action` what it does right now
- Reading a device with "ask" sends an approval request like an action (it was refused);
  one confirmation covers one read, the state is returned by `get_state` or later by
  `approval_status`

Temperatures in °F
- Households that use °F in Home Assistant can set temperatures and limit them in °F; the
  editor offers limits in the household's unit and flags a limit in the other one

Approvals
- A tap on the notification opens where the request can be answered (the open requests in
  the UI in direct mode, the Home-Mandate panel in the app); on Android the notification
  stays when tapped and comes back once after it was swiped away while the request is open;
  its second line says how to answer
- The approval request shows a temperature with its unit

UI
- Connecting an agent ends with the next steps, also on the browser sign-in page, which
  now shows the agent once it is admitted: check the mandate, whether approval requests
  reach anyone, and a first request

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
