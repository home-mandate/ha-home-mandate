// SPDX-License-Identifier: AGPL-3.0-or-later

import { tick } from 'svelte';
import { describe, expect, it } from 'vitest';
import { Announcer } from './announcer.svelte.ts';

describe('Announcer', () => {
  it('says texts of the same moment together instead of only the last (review L4)', async () => {
    const live = new Announcer();
    const done = Promise.all([live.say('One.'), live.say('Two.'), live.say('Three.')]);
    expect(live.text).toBe('');
    await done;
    expect(live.text).toBe('One. Two. Three.');
  });

  it('says the same text again: it empties the region first', async () => {
    const live = new Announcer();
    await live.say('Saved.');
    const again = live.say('Saved.');
    expect(live.text).toBe('');
    await again;
    await tick();
    expect(live.text).toBe('Saved.');
  });
});
