// SPDX-License-Identifier: AGPL-3.0-or-later

// Texts for screen readers when requests change live.

import type { ApprovalRequest } from '../api/types.ts';
import { m } from '../i18n.ts';
import { actionLabel } from '../mandate/labels.ts';
import { isolate } from '../untrusted.ts';

/**
 * openedText announces a new request: who wants what, agent and device cleaned and isolated,
 * and the agent's name marked as its claim like AgentName does (security review S6).
 */
export function openedText(request: ApprovalRequest): string {
  const agent = `${isolate(request.agent.display_name)}, ${m.agent_claim_short()}`;
  const title = m.request_title({ agent, action: actionLabel(undefined, request.action), device: isolate(request.device_name) });
  return m.request_opened_live({ title });
}
