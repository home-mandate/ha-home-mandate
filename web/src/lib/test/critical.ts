// SPDX-License-Identifier: AGPL-3.0-or-later

// A template whose rule allows a critical action without approval (decision U9), for the
// tests of the separate confirmation.
import type { MockClient } from '../api/mock.ts';
import { templatesFixture } from '../api/fixtures.ts';

export const DOORS = 'doors';

export async function addCriticalTemplate(api: MockClient): Promise<void> {
  const base = templatesFixture[0]!.draft;
  await api.putTemplate(DOORS, {
    draft: { ...base, rules: [{ id: 'r-unlock', resource: { category: 'lock' }, actions: ['unlock'], decision: 'allow', allow_critical: true }] },
    base_digest: null,
    confirm_critical: true,
  });
}
