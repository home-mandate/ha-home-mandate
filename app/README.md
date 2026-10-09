# Home-Mandate

Mandates for AI agents in Home Assistant: what an agent may do, where it has to ask a human
first, and what it may never do. Every decision is recorded in a tamper-evident audit log.

- **allow, ask, deny** per device, area and action, with time windows
- **Approval requests** on your phone through the Home Assistant Companion app
- **Emergency stop** for all agents at once
- **Audit log** with a hash chain and signed checkpoints

Agents connect over MCP with OAuth; Home-Mandate never hands them a Home Assistant token.

This is an experimental release candidate. See the Documentation tab before you install it.
