# Using Home-Mandate

This guide explains the ideas behind Home-Mandate, walks through the first setup in the
UI, and describes day-to-day operation and the command line. It applies to both ways of
running Home-Mandate; where they differ, it says so. Install first:
[install-ha-os.md](install-ha-os.md) or [install-container.md](install-container.md).

- [Concepts](#concepts)
- [First steps](#first-steps)
- [Day-to-day operation](#day-to-day-operation)
- [Command line](#command-line)

## Concepts

### Agent

An AI agent that connects to Home-Mandate over MCP: Claude Desktop, Claude Code, a local
language model, a script. Every agent gets its own identity when a Home Assistant
administrator admits it, and it acts only through Home-Mandate's four tools
([agents.md](agents.md#the-tools-agents-see)). It never gets a Home Assistant token.

The name an agent gives itself is its own claim. The UI marks such names with a dotted
underline ("Stated by the agent, unverified").

### Mandate

A mandate says what one agent may do. It consists of:

- **Rules.** Each rule names devices (all devices, a category, an area, or a single
  device), actions, a **decision** and optional conditions. Decisions are **Allow** (runs
  immediately), **Ask first** (a person must confirm) and **Deny**.
- **Everything else: denied.** A request no rule covers is denied. This default cannot be
  changed.
- **The strictest rule wins.** If several rules apply, deny beats ask beats allow. The
  order of the rules does not matter; it is only for reading.
- **Approvals:** who is asked (**Approvers**) and how long Home-Mandate waits
  (**Approval timeout**). A rule with *Ask first* can have its own timeout and approvers.
- **Rate limit:** at most so many requests per hour (every request counts, reading and
  listing included).
- **Validity:** valid from, and optionally valid until (that day included).

Every agent has at most one active mandate. Every change stores a new **version**; earlier
versions stay viewable and can be restored as a new version. A mandate change applies to
the agent's next request.

Writing does not include reading: a rule that allows `turn_on` does not allow `read`.

### Categories and actions

Home-Mandate takes a device's category from Home Assistant, never from the agent:

| Category (UI) | Home Assistant entities | Actions |
|---|---|---|
| Lights | `light.*` | read, turn on, turn off, set (brightness, colour temperature) |
| Switch | `switch.*` | read, turn on, turn off |
| Climate | `climate.*` | read, set temperature, set mode |
| Blinds | `cover.*` of other classes (blind, shade, shutter, curtain, awning, window …) | read, open, close, stop, set position |
| Gate/garage | `cover.*` of the class garage, gate or door, **and covers without a class** | read, **open**, close |
| Lock | `lock.*` | read, lock, **unlock**, **open** |
| Alarm | `alarm_control_panel.*` | read, arm (home, away, night), **disarm** |
| Camera | `camera.*` | read, **get snapshot** |
| Media | `media_player.*` | read, on, off, play, pause, volume |
| Sensor | `sensor.*`, `binary_sensor.*` | read |
| Scene | `scene.*` | read, **activate** |
| Script | `script.*` | read, **run** |
| Other | every other domain | read, **set** |

Actions in **bold** are critical (see below).

**Covers as gates.** A cover that may close an entrance (garage door, gate, door) is a
gate, and opening it is critical. Covers without a device class are gates too, because
cover groups and many template covers have none and could be a front door. So a rule that
allows "Blinds" never lets an agent open the garage. Give a blind without a class its
class in Home Assistant (for example `blind`) to manage it as a blind.

In this version, camera snapshots and `set` on devices of the category Other are evaluated
and logged but not executed.

### Critical devices and actions

Some actions are critical by nature: unlocking or opening a lock, opening a gate or garage
door, disarming the alarm, taking a camera snapshot, activating a scene, running a script,
setting a device of the category Other. In addition, you can **mark devices as critical**
(Settings → Critical devices): on a marked device every action except reading is critical.
Home-Mandate suggests candidates by name and device class (door, gate, garage), but marks
nothing by itself.

A critical action needs an approval even where a rule allows it ("Downgraded: Becomes an
approval request because the action is critical"), unless the rule explicitly says **Also
allow critical actions without approval**. Switching that on needs a separate
confirmation, and the audit log records who did it.

Critical approval requests go only to devices where **Critical requests too** is on (see
approvers below).

### Time windows, weekdays and limits

Under **Conditions (optional)** a rule can apply only within a **Time window** (also
overnight, such as 22:00 to 06:00 the next day) and on chosen **Weekdays**. Times are in
the household's time zone from Home Assistant, not your browser's.

An allowing rule can carry **Limits**: brightness, temperature, position or volume from
and to a value. Values outside the limits are denied. Temperature limits need a household
that uses °C in Home Assistant; in °F households such a rule does not match.

### Templates

A template is a mandate without agent, name and validity. When you admit an agent you pick
a template, and it becomes the agent's mandate. Three **built-in templates** ship with
Home-Mandate:

| Template | Allows |
|---|---|
| Read only (`hm-read-only`) | Reading the state of every device; nothing is switched. |
| Light and climate (`hm-light-climate`) | Reading everything; switching and dimming lights; setting temperatures; opening, closing and positioning blinds. Gates, garage doors, doors and covers without a class are not included. |
| Voice assistant (cautious) (`hm-voice-cautious`) | Reading everything except cameras; switching lights; setting temperatures; unlocking and opening locks only after an approval; never cameras, never disarming the alarm. |

All three allow 60 requests per hour and wait 2 minutes for an approval. Built-in
templates cannot be changed or deleted, but you can hide them and save them as your own
template under a new name. Names starting with `hm-` are reserved for them.

In a template, the approvers are "The household's approvers and whoever admits the agent":
when an agent is admitted, they become the people set up under Settings → Approvers plus
the administrator who admits it. The mandate keeps that list: approvers you set up later
are not added to existing mandates; edit the mandate to add them.

Changing a template does not change existing mandates. After you save a template,
Home-Mandate offers to take the change over into the mandates made from it.

### Approvers and their devices

Approvers are people from Home Assistant (persons with a user account) who answer approval
requests. For each approver you choose:

- **Devices with the Home Assistant app**: up to 5 devices with the Companion app (the
  `notify.mobile_app_…` services). Other notify services (groups, messengers,
  `notify.notify`) are refused, because they could reach people who are no approvers.
- **Critical requests too**, per device. iPhones and iPads ask for unlocking before a
  button in a notification counts, so they are suited. Android phones and the Mac app do
  not; whoever holds the unlocked device can approve. The UI proposes it only for iOS
  devices of the person themselves.
- **Notification language**: German or English; without a choice, the language of Home
  Assistant's configuration.
- **Answer in Home-Mandate** (administrators only): the person can also answer in the
  Home-Mandate UI while it is open; **Critical requests in Home-Mandate too** separately,
  because a computer asks for no unlocking.

The Companion app on an approver's device must be signed in with **that approver's own
Home Assistant account**: Home-Mandate checks who answered. An answer from anyone else is
discarded, denies the request at once and warns the approvers.

### Approval requests

When a request needs an approval, Home-Mandate sends a notification to every device of
every approver of the mandate. It reads "Approval needed: *agent*", says what the agent
wants to do on which device, shows the reason the agent gave as "Agent's claim, not
verified", the values it wants to set, and has two buttons: **Allow** and **Deny**.

- The first answer counts, from any channel.
- No answer within the approval timeout, a refusal, an answer from someone who may not
  approve, or no approver who can be reached means **deny**.
- The agent's request waits for the answer. The mandate's timeout (10 seconds to 1 hour in
  the editor) is capped by the installation's approval timeout: the app option
  `approval_timeout_seconds` or `HM_APPROVAL_TIMEOUT`, 30 to 600 seconds, default 120.
- After an approval Home-Mandate checks the emergency stop, the agent's token and the
  mandate again before it acts.
- An agent can have at most 2 approval requests waiting.
- **Cooldown after refusal:** after a refusal, a timeout or an invalid answer, the same
  agent may not ask again for the same device for 1 minute, then 2, 4 … up to 1 hour for
  each further one. An approval ends the cooldown, and so does an hour without a new
  refusal. Nobody is notified during a cooldown; the agent gets `approval_cooldown`.
- Open approval requests live in memory; a restart of Home-Mandate ends them, and nothing
  is executed.

Optionally, **Also in Home Assistant's notification bell** shows a neutral hint ("A
request is waiting in Home-Mandate") in Home Assistant, without agent, device or reason,
because every Home Assistant user can see that bell.

### Emergency stop

The emergency stop blocks every agent at once: all tokens are revoked, pending approvals
are declined, and every request is refused until you lift it. Home Assistant itself keeps
running normally.

After you lift it, every agent has to be admitted again (agents with a pairing code need
a new code). Each admission creates a new entry under Agents with a new mandate from the
chosen template; the earlier entry and its mandate stay, without access. Revoke the old
entry if you no longer need it.

### Audit log

Every request, decision, approval, admission, mandate change and setting that matters is
written to the audit log, together with who did it. Entries are kept for 30 days; the
daily cleanup is itself an entry.

The log is tamper-evident:

- Every entry contains the hash of the previous one (a hash chain). Changing or deleting
  an entry in the middle breaks the chain from there on.
- Every 15 minutes, and after every cleanup, Home-Mandate signs a **checkpoint** with its
  own key. `audit verify` reports how far the log is anchored by checkpoints.
- Once a day the position and digest of the log go to the approvers' devices as a
  notification ("Home-Mandate: state of the audit log"). Those notifications cannot be
  taken back from the phones; keep them as evidence.
- The UI verifies the chain every 10 minutes and on **Verify now**.

What it cannot do: someone who can write Home-Mandate's data directory, and who also has
the checkpoint key, can write a new consistent log. Keep the key away from the data
directory if that matters to you ([install-container.md](install-container.md#backup-and-restore)).

## First steps

The UI has five sections in its header: **Overview**, **Agents**, **Mandates**, **Audit
log** and **Settings**, plus the **Emergency stop** button. The interface language follows
your browser; change it under Settings → Defaults → Interface language.

The order below avoids the most common surprise: an agent admitted before anyone can
answer approval requests gets every *Ask first* action denied.

### 1. Set up approvers

**Settings → Approvers → Add person**, choose the **Person from Home Assistant**.

1. Under **Devices with the Home Assistant app**, **Add device** for each phone or tablet of
   that person with the Companion app.
2. Leave **Critical requests too** on for an iPhone or iPad of that person, off for
   Android, computers and devices of others.
3. Choose the **Notification language** if it should differ from Home Assistant's.
4. **Send test** and check that the test notification ("Home-Mandate: test") arrives.

Add a second person if you can: with only one, the page says "Approvals go to one person
only". The section summarises whether normal and critical requests reach someone; read
that line.

Changes on this page are saved immediately.

### 2. Mark critical devices

**Settings → Critical devices.** Look at the **Suggestions** (names or device classes that
point to a door, gate or garage) and mark what really is critical. Use **Find another
device** for anything the suggestions miss, such as a smart plug that powers a door
opener. Marking is only protection once it is done; nothing is marked automatically.

### 3. Pick or adjust a template

**Mandates → Templates** lists the **Built-in templates** and **Your templates**. Each card
says what the template allows ("May", "Asks you first", "Never"; "Everything else is
forbidden").

To adapt one: **Load template …** opens it in the editor, change the rules, then **Save
as new template …** with a name of lowercase letters, digits and hyphens (for example
`guest-room`). **New template** starts from scratch. **Hide** keeps a template from being
offered when admitting agents.

The editor's **Preview** tab shows, per device, what an agent with these rules may do and
which rule decides.

### 4. Admit your first agent

**Agents → Add agent** asks "How does your agent sign in?":

- **With browser sign-in** (Claude Code, Claude Desktop with `mcp-remote`, claude.ai): the
  page shows the **MCP endpoint address** to enter in your agent. The agent opens a page in
  your browser; you sign in with your Home Assistant account, check the agent's identity,
  choose the mandate template and **Admit**. Details per agent: [agents.md](agents.md).
- **With a pairing code** (agents without a browser): the agent shows a code such as
  `BCDF-GHJK`. Enter it under **Pairing code**, **Check code**, compare the client ID
  with what your agent shows ("Is this the right agent?"), choose the mandate and
  **Approve agent**. If you did not start this pairing yourself, choose **This isn't my
  agent**.

Either way, only continue if you started the connection yourself just now. The consent and
pairing pages show **Who may approve** for the chosen template and warn if nobody can
answer approval requests yet.

The agent then appears under **Agents**, and its mandate under **Mandates**. Open the
mandate to adjust it; saving shows a summary ("Save changes?") of what changes before it
stores a new version.

### 5. Answer an approval request

When the agent asks for something with *Ask first* (for example unlocking with the
cautious voice assistant template), your phone shows the notification. Long-press or
expand it (depending on the phone) to see **Allow** and **Deny**. On an iPhone, the phone
asks you to unlock it first for a critical action.

The agent's call waits for your answer and then gets the result. Pending requests also
appear on the **Overview** under **Pending approvals**; if **Answer in Home-Mandate** is on
for you, you can answer there (**Approve** → **Yes, approve**, or **Decline**).

### 6. Try the emergency stop

Choose **Emergency stop** in the header, read the dialog and confirm **Trigger emergency
stop**. A banner "Emergency stop active" appears, and every agent's next request is
refused. Lift it with **Lift …** in the banner or **Settings → Emergency stop → Lift
emergency stop …**, then admit the agent again (see [Emergency stop](#emergency-stop)).

### 7. Read the audit log

**Audit log** shows all entries of the retention period ("All (30 days)") by default. The tab
**Events** lists everything, **Approvals** only approval requests. Filter by **Period**,
**Agent**, **Decision**, **Event type**, or search by device, area or agent. Select an entry
for its details: decision, the rule that decided, the requested values, who approved, and
under **Technical details** its hash and the hash of the previous entry.

At the top, the page shows whether the chain is intact ("Audit log complete and
unaltered"); **Verify now** checks it again.

## Day-to-day operation

### Overview

The **Overview** shows the state at a glance: active agents, pending approvals, the
emergency stop and the connection to Home Assistant. Banners under the header report what
needs attention: emergency stop active, Home Assistant unreachable, a certificate that
expires, a broken audit chain, a wrong host clock, devices renamed in Home Assistant.

### Agents

**Agents** lists every admitted agent with its status and last activity. An agent's page
shows its identity, how it signed in (browser or pairing code), its mandate, its rate limit
usage and a link to its audit entries.

- **Take over rules from a template** gives the mandate a new version with the template's
  rules, approval settings and rate limit.
- **End access → Revoke access** blocks the agent immediately and permanently, revokes its
  tokens and mandate, and declines its pending approvals. To use it again, admit it anew.

### Mandates

**Mandates** lists every mandate with its agent, rules, status and validity. Open one to
edit:

- **Basics**: display name, assigned agent, **Valid from**, **Valid until (optional)**.
- **Approvals**: **Approval timeout** and **Approvers** (at least one person).
- **Rate limit**: actions per hour.
- **Rules**: **Add rule** (new rules start with *Ask first*), then decision, category, area
  or **Single device (optional)**, actions, **Conditions (optional)** and limits.

**Save …** shows the changed rules and their effect on permissions before it stores a new
version. **Versions** lists all versions with author and time; compare two or **Restore as
new version**.

**Renamed in Home Assistant.** If you rename an entity in Home Assistant, rules on its
former ID keep applying to it until you decide: **Take over** stores new versions of the
affected mandates with the new ID, **Don't take over** lets those rules go. Until then the
stricter decision of old and new rules wins, so a deny or ask rule keeps protecting the
device. A rule on an area covers the devices that are in that area now; a device moved
elsewhere leaves it.

### Settings

| Section | What it is for |
|---|---|
| Approvers | People, their devices, critical requests, language, answering in the UI, the notification bell, browser notifications |
| Critical devices | Devices on which every action except reading is critical |
| Defaults | Approval timeout and rate limit that new templates and mandates start with; interface language |
| Home Assistant connection | Connection state, Home Assistant version, Home-Mandate's own Home Assistant user, why it needs admin rights and the fixed command list |
| MCP endpoint | The address agents connect to; the TLS certificate or the reverse proxy |
| Retention | 30 days for the audit log |
| Emergency stop | Trigger or lift it |
| About | Version, commit, license, source code, licenses of the included packages |

## Command line

The administration commands work on the local database only. They are not reachable over
the network and need no Home Assistant credentials, only the data directory. They can run
while Home-Mandate runs; changes take effect with the next request.

Where to run them:

- **Container mode:** `docker exec home-mandate /home-mandate <command>` (add `-i` for
  commands that read standard input).
- **Home Assistant OS app:** the commands are in the app's container, which Home Assistant
  OS does not open to you by default. Everything you need day to day is in the UI.

| Command | What it does |
|---|---|
| `home-mandate -version` | Prints the version. |
| `home-mandate serve` | Runs the gateway; the default without a command. |
| `home-mandate household` | Prints the household's principal, the value of `principal` in mandates. |
| `home-mandate agent list` | Lists agents: client ID, status, display name (tab-separated). |
| `home-mandate agent revoke CLIENT_ID` | Revokes an agent and all its tokens; its next request is refused. |
| `home-mandate emergency-stop on` | Triggers the emergency stop: all tokens revoked, all agents blocked. |
| `home-mandate emergency-stop off` | Lifts it; agents need new tokens. |
| `home-mandate emergency-stop status` | Prints `emergency stop: on` or `off`. |
| `home-mandate approver add USER_ID SERVICE[:no-critical][,SERVICE…] [de\|en]` | Adds or replaces an approver: their Home Assistant user ID, up to 5 Companion app notify services such as `mobile_app_pixel_9` (without `notify.`), `:no-critical` for devices that should not get critical requests, and optionally the language. Keeps the "answer in the UI" settings, which only the UI changes. |
| `home-mandate approver list` | Lists approvers: user ID, devices, language, UI channel. |
| `home-mandate approver remove USER_ID` | Removes an approver. |
| `home-mandate mandate import FILE` (or `-` for standard input) | Stores a mandate (JSON, validated against the Home-Mandate specification); prints `id`, `client_id` and `digest`. |
| `home-mandate mandate list` | Lists mandates: ID, status, client ID, digest. |
| `home-mandate mandate revoke ID` | Revokes a mandate. |
| `home-mandate mandate check` | Lists stored mandates and templates this version does not accept (an invalid mandate denies every request of its agent); prints `ok` if there are none. Useful after an update. |
| `home-mandate mandate template import NAME FILE` (or `-`) | Stores a template under `NAME`. |
| `home-mandate mandate template list` | Lists templates (built-in ones as `base template`, hidden ones marked). |
| `home-mandate mandate template remove NAME` | Removes a template of your own. |
| `home-mandate audit verify` | Verifies the hash chain and checkpoints. Prints `audit log valid` with `entries`, `anchored_up_to`, `first_seq`, `truncation` and `log_id`, or where the log is broken; exits with status 1 if it is broken. |
| `home-mandate audit export` | Writes the audit log as JSON Lines to standard output. |
| `home-mandate audit key` | Prints the log ID and the public key of the checkpoints (JWKS). Keep them outside this device, so that anyone can verify an exported log later. Works only after the first start of `serve`, which creates the key. |
| `home-mandate audit accept-clock` | After the host clock ran ahead by mistake and was corrected: sets the entries with future times aside for the clock check, so that requests are decided again. See [troubleshooting.md](troubleshooting.md#the-clock-is-behind). |

Changes made on the command line appear in the audit log with the actor `local-admin`.
The emergency stop and revocations from the command line take effect at once; approval
requests already waiting are not ended by them, but nothing is executed after an answer
(Home-Mandate checks again before it acts).

Mandate and template files follow the Home-Mandate specification (the `home-mandate/spec`
repository). A template is a mandate whose `id`, `principal`, `agent`, `created_by`,
`created_at`, `valid_from` and `expires` are filled in when an agent is admitted. The UI
editor is the easier way; the files are for automation.
