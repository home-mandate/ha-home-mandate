// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from '../api/client.ts';
import { m } from '../i18n.ts';

/**
 * answerError says what a failed answer means. Without a clear answer from the server (not
 * reached, 5xx from it or a proxy, or no API error at all) the answer may still have
 * arrived, so it never claims it failed. Errors the client raised before sending (such as
 * a missing CSRF token) and refusals mean nothing was sent.
 */
export function answerError(err: unknown): string {
  if (!(err instanceof ApiError) || err.code === 'unavailable' || err.status >= 500) return m.request_answer_unknown();
  if (err.code === 'not_found' || err.code === 'conflict') return m.request_gone();
  return m.request_answer_failed();
}
