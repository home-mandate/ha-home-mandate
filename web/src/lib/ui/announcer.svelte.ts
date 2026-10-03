// SPDX-License-Identifier: AGPL-3.0-or-later

// Text for a page's live region. The region is emptied first, so the same text twice in a
// row is said again; texts that come in the same moment (several events in one tick) are
// said together instead of the last one replacing the others (review L4).

import { tick } from 'svelte';

export class Announcer {
  text = $state('');
  #waiting: readonly string[] = [];

  async say(text: string): Promise<void> {
    this.#waiting = [...this.#waiting, text];
    if (this.#waiting.length > 1) return;
    this.text = '';
    await tick();
    this.text = this.#waiting.join(' ');
    this.#waiting = [];
  }
}
