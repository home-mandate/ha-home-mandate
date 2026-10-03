// SPDX-License-Identifier: AGPL-3.0-or-later

// Service data of an approval request (security review S1): the human sees in the UI what
// the push shows on the phone, e.g. "brightness_pct=100". Names and values come from the
// agent, so they are cleaned like other agent text and cut to the push's 80 characters.

import type { ApprovalParam } from '../api/types.ts';
import { cleanUntrusted } from '../untrusted.ts';

/** Longest name or value, as in the push (internal/approval maxName). */
export const PARAM_MAX = 80;

/** paramPair is one cleaned "name=value". */
export function paramPair(param: ApprovalParam): string {
  return `${cleanUntrusted(param.name, PARAM_MAX)}=${cleanUntrusted(param.value, PARAM_MAX)}`;
}

/** paramsText joins the pairs in the server's order, like the push; empty without service data. */
export function paramsText(params: readonly ApprovalParam[]): string {
  return params.map(paramPair).join(', ');
}
