// SPDX-License-Identifier: AGPL-3.0-or-later

// One way to start connecting an agent (issue #15): every entry point leads to the agents page
// with the choice of both ways open. Only the choice itself links to a way directly.

import { describe, expect, it } from 'vitest';

const sources = import.meta.glob<string>(['../**/*.svelte', '../**/*.ts', '!../**/*.test.ts', '!./paraglide/**'], {
  query: '?raw',
  import: 'default',
  eager: true,
});

const CHOICE = './components/agents/AddWays.svelte';
const DIRECT = [/name:\s*'(pair|connect)'\s*}/, /#\/agents\/(pair|browser)\b/];

describe('entry points to connect an agent', () => {
  it('finds the sources it checks', () => {
    expect(Object.keys(sources)).toContain(CHOICE);
    expect(Object.keys(sources)).toContain('./components/overview/Onboarding.svelte');
    expect(Object.keys(sources)).toContain('../App.svelte');
  });

  it('link to a way of signing in only from the choice of ways', () => {
    const direct = Object.entries(sources)
      .filter(([path]) => path !== CHOICE && path !== './router.ts')
      .filter(([, text]) => DIRECT.some((pattern) => pattern.test(text)))
      .map(([path]) => path);
    expect(direct).toEqual([]);
  });
});
