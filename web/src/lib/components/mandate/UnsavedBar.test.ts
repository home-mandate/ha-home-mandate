// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { cleanup, fireEvent, render, screen } from '@testing-library/svelte';
import { setLocale } from '../../paraglide/runtime.js';
import { SAVEBAR_CLASS, SAVEBAR_SIZE } from '../../ui/savebar.ts';
import UnsavedBar from './UnsavedBar.svelte';

beforeEach(() => setLocale('en', { reload: false }));
afterEach(() => {
  cleanup();
  document.documentElement.classList.remove(SAVEBAR_CLASS);
});

function show(count = 2) {
  const onsave = vi.fn();
  const ondiscard = vi.fn();
  const view = render(UnsavedBar, { count, note: 'They apply only after saving.', saveLabel: 'Save changes …', onsave, ondiscard });
  return { onsave, ondiscard, view };
}

describe('UnsavedBar', () => {
  it('names the count and what it means, as a labelled region', () => {
    show(2);
    const region = screen.getByRole('region', { name: '2 unsaved changes' });
    expect(region.textContent).toContain('They apply only after saving.');
  });

  it('saves and discards with its buttons', async () => {
    const { onsave, ondiscard } = show();
    await fireEvent.click(screen.getByRole('button', { name: 'Save changes …' }));
    expect(onsave).toHaveBeenCalledTimes(1);
    await fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
    expect(ondiscard).toHaveBeenCalledTimes(1);
  });

  it('marks the page while it is shown, so the page keeps room for it', () => {
    const { view } = show();
    expect(document.documentElement.classList.contains(SAVEBAR_CLASS)).toBe(true);
    view.unmount();
    expect(document.documentElement.classList.contains(SAVEBAR_CLASS)).toBe(false);
  });

  it('keeps its height on the page while it is shown, without moving the page', () => {
    document.documentElement.scrollTop = 0;
    const { view } = show();
    expect(document.documentElement.style.getPropertyValue(SAVEBAR_SIZE)).toMatch(/^\d+px$/);
    view.unmount();
    expect(document.documentElement.style.getPropertyValue(SAVEBAR_SIZE)).toBe('');
    expect(document.documentElement.scrollTop).toBe(0);
  });

  it('follows the count', async () => {
    const { view } = show(1);
    expect(screen.getByRole('region', { name: '1 unsaved change' })).toBeTruthy();
    await view.rerender({ count: 3 });
    expect(screen.getByRole('region', { name: '3 unsaved changes' })).toBeTruthy();
  });
});
